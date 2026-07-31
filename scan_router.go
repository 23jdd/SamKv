package main

// 本文件暴露 /scan 接口，支持通过 start 和 end 查询参数进行键范围扫描。
// 区间为 [start, end)，空 start 表示无下界，空 end 表示无上界。

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/gin-gonic/gin"
)

// ScannerStore 定义键范围扫描所需的存储能力。
type ScannerStore interface {
	Scan(startKey, endKey string) ([]store.Record, error)
}

type scanHandler struct {
	store ScannerStore
}

type scanRecordResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type scanResponse struct {
	Records []scanRecordResponse `json:"records"`
}

func registerScanRoutes(router *gin.Engine, database ScannerStore) {
	handler := &scanHandler{store: database}
	router.GET("/scan", handler.scan)
}

// scan 通过查询参数 start 和 end 获取 [start, end) 范围内的 KV 记录。
// 两个参数均可省略：空 start 表示从最小键开始，空 end 表示扫描到最大键。
func (h *scanHandler) scan(c *gin.Context) {
	startKey, err := url.QueryUnescape(c.Query("start"))
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid start key encoding")
		return
	}
	endKey, err := url.QueryUnescape(c.Query("end"))
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid end key encoding")
		return
	}

	records, err := h.store.Scan(startKey, endKey)
	if err != nil {
		if errors.Is(err, store.ErrInvalidRange) {
			writeJSONError(c, http.StatusBadRequest, err.Error())
			return
		}
		writeStoreError(c, err)
		return
	}

	response := scanResponse{
		Records: make([]scanRecordResponse, 0, len(records)),
	}
	for _, record := range records {
		response.Records = append(response.Records, scanRecordResponse{
			Key:   record.Key,
			Value: record.Val,
		})
	}
	c.JSON(http.StatusOK, response)
}
