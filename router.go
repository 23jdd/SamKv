package main

// 本文件定义 Gin KV 路由、严格 JSON 解码、请求大小限制和 Store 错误到 HTTP 状态码的映射。
// key 来自 /kv/*key，因此可以包含斜杠；空 key 返回 400，底层读取错误不会伪装为 404。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/gin-gonic/gin"
)

const (
	maxRequestBodyBytes = 64 << 20
	maxKVRecordBytes    = 64 << 20
	walFixedPayloadSize = 17
)

// KVStore 描述 HTTP 层使用的最小 KV 能力，便于路由测试和替换实现。
type KVStore interface {
	Put(key, value string) error
	Get(key string) (string, bool)
	Delete(key string) error
	BackgroundError() error
}

// KVStoreWithError 允许 HTTP 层区分 key 不存在和底层 SSTable 读取失败。
type KVStoreWithError interface {
	GetWithError(key string) (string, bool, error)
}

type kvHandler struct {
	store KVStore
}

type putKVRequest struct {
	Value *string `json:"value"`
}

type kvResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// NewRouter 创建暴露 KV 读写、删除和健康检查接口的 Gin 路由。
// database 不能为 nil；请求体和单条 WAL 记录限制为 64 MiB，JSON 不允许未知字段或多个顶层对象。
func NewRouter(database KVStore) *gin.Engine {
	if database == nil {
		panic("router: nil store")
	}
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), corsMiddleware())
	router.HandleMethodNotAllowed = true

	handler := &kvHandler{store: database}
	router.GET("/healthz", handler.health)

	// 精确路由用于把缺少 key 的请求转换为稳定的 400 响应。
	router.GET("/kv", missingKey)
	router.PUT("/kv", missingKey)
	router.DELETE("/kv", missingKey)
	router.GET("/kv/*key", handler.get)
	router.PUT("/kv/*key", handler.put)
	router.DELETE("/kv/*key", handler.delete)

	if logDatabase, ok := database.(LogStore); ok {
		registerLogRoutes(router, logDatabase)
	}
	if metricsDatabase, ok := database.(MetricsStore); ok {
		registerMetricsRoute(router, metricsDatabase)
	}
	if scanDatabase, ok := database.(ScannerStore); ok {
		registerScanRoutes(router, scanDatabase)
	}

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, errorResponse{Error: "route not found"})
	})
	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
	})
	return router
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		headers.Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (h *kvHandler) health(c *gin.Context) {
	if err := h.store.BackgroundError(); err != nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "store background maintenance failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *kvHandler) put(c *gin.Context) {
	key, ok := requestKey(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var request putKVRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(c, requestErrorStatus(err), "invalid JSON body")
		return
	}
	if request.Value == nil {
		writeJSONError(c, http.StatusBadRequest, "value is required")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(c, http.StatusBadRequest, "body must contain one JSON object")
		return
	}
	if len(key)+len(*request.Value)+walFixedPayloadSize > maxKVRecordBytes {
		writeJSONError(c, http.StatusRequestEntityTooLarge, "key and value exceed the WAL record limit")
		return
	}

	if err := h.store.Put(key, *request.Value); err != nil {
		writeStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *kvHandler) get(c *gin.Context) {
	key, ok := requestKey(c)
	if !ok {
		return
	}
	var (
		value string
		found bool
		err   error
	)
	if database, ok := h.store.(KVStoreWithError); ok {
		value, found, err = database.GetWithError(key)
	} else {
		value, found = h.store.Get(key)
	}
	if err != nil {
		writeStoreError(c, err)
		return
	}
	if !found {
		writeJSONError(c, http.StatusNotFound, "key not found")
		return
	}
	c.JSON(http.StatusOK, kvResponse{Key: key, Value: value})
}

func (h *kvHandler) delete(c *gin.Context) {
	key, ok := requestKey(c)
	if !ok {
		return
	}
	if err := h.store.Delete(key); err != nil {
		writeStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func requestKey(c *gin.Context) (string, bool) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		missingKey(c)
		return "", false
	}
	return key, true
}

func missingKey(c *gin.Context) {
	writeJSONError(c, http.StatusBadRequest, "key is required")
}

func requestErrorStatus(err error) int {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func writeStoreError(c *gin.Context, err error) {
	_ = c.Error(err)
	if errors.Is(err, store.ErrStoreClosed) || errors.Is(err, store.ErrBackgroundFailure) {
		writeJSONError(c, http.StatusServiceUnavailable, "store unavailable")
		return
	}
	writeJSONError(c, http.StatusInternalServerError, "store operation failed")
}

func writeJSONError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, errorResponse{Error: message})
}
