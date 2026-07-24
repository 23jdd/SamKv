package main

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
func NewRouter(database KVStore) *gin.Engine {
	if database == nil {
		panic("router: nil store")
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
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

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, errorResponse{Error: "route not found"})
	})
	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
	})
	return router
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
	value, found := h.store.Get(key)
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
