package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/23jdd/SamKv/pkg/store"
)

func TestMetricsEndpointReportsStoreOperations(t *testing.T) {
	router := newTestRouter(t)
	if response := performRequest(router, http.MethodPut, "/kv/key", `{"value":"value"}`); response.Code != http.StatusNoContent {
		t.Fatalf("PUT status=%d", response.Code)
	}
	if response := performRequest(router, http.MethodGet, "/kv/key", ""); response.Code != http.StatusOK {
		t.Fatalf("GET status=%d", response.Code)
	}
	response := performRequest(router, http.MethodGet, "/metrics", "")
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != prometheusContentType {
		t.Fatalf("Content-Type = %q", contentType)
	}
	for _, metric := range []string{
		"samkv_write_operations_total 1",
		"samkv_read_operations_total 1",
		"samkv_background_error 0",
		"samkv_block_cache_hits_total",
	} {
		if !strings.Contains(response.Body.String(), metric) {
			t.Fatalf("metrics does not contain %q:\n%s", metric, response.Body.String())
		}
	}
}

func TestFormatPrometheusMetricsSortsLevels(t *testing.T) {
	output := formatPrometheusMetrics(store.Stats{
		LevelTables: map[int]int{2: 3, 0: 1, 1: 2},
	})
	level0 := strings.Index(output, `level="0"`)
	level1 := strings.Index(output, `level="1"`)
	level2 := strings.Index(output, `level="2"`)
	if level0 < 0 || level1 <= level0 || level2 <= level1 {
		t.Fatalf("level metrics are not sorted:\n%s", output)
	}
}
