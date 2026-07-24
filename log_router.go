package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/23jdd/SamKv/pkg/parse"
	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
	"github.com/gin-gonic/gin"
)

const (
	maxLogBatchEntries = 10_000
	defaultQueryLimit  = 1_000
	maxQueryLimit      = 10_000
)

// LogStore 定义结构化日志 HTTP 接口依赖的存储能力。
type LogStore interface {
	WriteLog(entry store.LogEntry) (uint64, error)
	WriteLogs(entries []store.LogEntry) ([]uint64, error)
	Query(startTime, endTime time.Time, labels []utils.Label) ([]store.LogEntry, error)
}

type logHTTPHandler struct {
	store LogStore
	now   func() time.Time
}

type logWriteRequest struct {
	Timestamp *time.Time        `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
	Message   *string           `json:"message"`
	Sequence  uint64            `json:"sequence,omitempty"`
}

type logBatchWriteRequest struct {
	Entries []logWriteRequest `json:"entries"`
}

type logWriteResponse struct {
	Sequence uint64 `json:"sequence"`
}

type logBatchWriteResponse struct {
	Sequences []uint64 `json:"sequences"`
}

type logEntryResponse struct {
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
	Message   string            `json:"message"`
	Sequence  uint64            `json:"sequence"`
}

type logQueryResponse struct {
	Matcher   string             `json:"matcher"`
	Start     time.Time          `json:"start"`
	End       time.Time          `json:"end"`
	Entries   []logEntryResponse `json:"entries"`
	Truncated bool               `json:"truncated"`
}

func registerLogRoutes(router *gin.Engine, database LogStore) {
	handler := &logHTTPHandler{store: database, now: time.Now}
	router.POST("/logs", handler.write)
	router.POST("/logs/batch", handler.writeBatch)
	router.GET("/logs/query", handler.query)
}

func (handler *logHTTPHandler) write(c *gin.Context) {
	var request logWriteRequest
	if !decodeStrictJSON(c, &request) {
		return
	}
	entry, ok := handler.requestEntry(c, request)
	if !ok {
		return
	}
	sequence, err := handler.store.WriteLog(entry)
	if err != nil {
		writeLogError(c, err)
		return
	}
	c.JSON(http.StatusCreated, logWriteResponse{Sequence: sequence})
}

func (handler *logHTTPHandler) writeBatch(c *gin.Context) {
	var request logBatchWriteRequest
	if !decodeStrictJSON(c, &request) {
		return
	}
	if len(request.Entries) == 0 || len(request.Entries) > maxLogBatchEntries {
		writeJSONError(c, http.StatusBadRequest, "entries must contain between 1 and 10000 logs")
		return
	}
	entries := make([]store.LogEntry, 0, len(request.Entries))
	for _, item := range request.Entries {
		entry, ok := handler.requestEntry(c, item)
		if !ok {
			return
		}
		entries = append(entries, entry)
	}
	sequences, err := handler.store.WriteLogs(entries)
	if err != nil {
		writeLogError(c, err)
		return
	}
	c.JSON(http.StatusCreated, logBatchWriteResponse{Sequences: sequences})
}

func (handler *logHTTPHandler) query(c *gin.Context) {
	rawQuery := c.Query("query")
	if rawQuery == "" {
		writeJSONError(c, http.StatusBadRequest, "query is required")
		return
	}
	query, err := parse.ParseQueryFormat(rawQuery)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := queryLimit(c.Query("limit"))
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	start, end := query.TimeRange(handler.now().UTC())
	labels := make([]utils.Label, 0, len(query.Labels))
	for _, label := range query.Labels {
		labels = append(labels, utils.Label{Name: label.Name, Value: label.Value})
	}
	entries, err := handler.store.Query(start, end, labels)
	if err != nil {
		writeLogError(c, err)
		return
	}

	response := logQueryResponse{
		Matcher: query.Query,
		Start:   start,
		End:     end,
		Entries: make([]logEntryResponse, 0, min(len(entries), limit)),
	}
	matcher := []byte(query.Query)
	for _, entry := range entries {
		if !bytes.Contains(entry.Message, matcher) {
			continue
		}
		if len(response.Entries) == limit {
			response.Truncated = true
			break
		}
		response.Entries = append(response.Entries, newLogEntryResponse(entry))
	}
	c.JSON(http.StatusOK, response)
}

func (handler *logHTTPHandler) requestEntry(c *gin.Context, request logWriteRequest) (store.LogEntry, bool) {
	if request.Message == nil {
		writeJSONError(c, http.StatusBadRequest, "message is required")
		return store.LogEntry{}, false
	}
	timestamp := handler.now().UTC()
	if request.Timestamp != nil {
		timestamp = request.Timestamp.UTC()
	}
	return store.LogEntry{
		Timestamp: timestamp,
		Labels:    sortedLabels(request.Labels),
		Sequence:  request.Sequence,
		Message:   []byte(*request.Message),
	}, true
}

func sortedLabels(labels map[string]string) []utils.Label {
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]utils.Label, 0, len(names))
	for _, name := range names {
		result = append(result, utils.Label{Name: name, Value: labels[name]})
	}
	return result
}

func newLogEntryResponse(entry store.LogEntry) logEntryResponse {
	labels := make(map[string]string, len(entry.Labels))
	for _, label := range entry.Labels {
		labels[label.Name] = label.Value
	}
	return logEntryResponse{
		Timestamp: entry.Timestamp,
		Labels:    labels,
		Message:   string(entry.Message),
		Sequence:  entry.Sequence,
	}
}

func queryLimit(raw string) (int, error) {
	if raw == "" {
		return defaultQueryLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxQueryLimit {
		return 0, errors.New("limit must be between 1 and 10000")
	}
	return limit, nil
}

func decodeStrictJSON(c *gin.Context, destination any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSONError(c, requestErrorStatus(err), "invalid JSON body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(c, http.StatusBadRequest, "body must contain one JSON object")
		return false
	}
	return true
}

func writeLogError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrInvalidTimeRange) ||
		errors.Is(err, store.ErrDuplicateLabel) ||
		errors.Is(err, utils.ErrInvalidLabel) {
		writeJSONError(c, http.StatusBadRequest, err.Error())
		return
	}
	writeStoreError(c, err)
}
