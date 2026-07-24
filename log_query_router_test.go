package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestLogQueryUsesTimeLabelsAndMatcher(t *testing.T) {
	router := newTestRouter(t)
	timestamp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	writeLogForQueryTest(t, router, timestamp, map[string]string{"app": "nginx", "level": "ERROR"}, "upstream failed")
	writeLogForQueryTest(t, router, timestamp, map[string]string{"app": "nginx", "level": "INFO"}, "upstream healthy")
	writeLogForQueryTest(t, router, timestamp, map[string]string{"app": "api", "level": "ERROR"}, "upstream failed")

	expression := `"upstream failed"{app=nginx}[1h]`
	response := performRequest(router, http.MethodGet, "/logs/query?query="+url.QueryEscape(expression), "")
	if response.Code != http.StatusOK {
		t.Fatalf("query status=%d body=%s", response.Code, response.Body.String())
	}
	var result logQueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Matcher != "upstream failed" || result.Truncated || len(result.Entries) != 1 {
		t.Fatalf("query response = %#v", result)
	}
	entry := result.Entries[0]
	if entry.Message != "upstream failed" || entry.Labels["app"] != "nginx" || entry.Labels["level"] != "ERROR" {
		t.Fatalf("query entry = %#v", entry)
	}
}

func TestLogQueryReportsTruncationAfterMatcherFiltering(t *testing.T) {
	router := newTestRouter(t)
	timestamp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	for i := 0; i < 2; i++ {
		writeLogForQueryTest(t, router, timestamp, map[string]string{"app": "api"}, fmt.Sprintf("error %d", i))
	}
	expression := `error{app=api}[1h]`
	response := performRequest(router, http.MethodGet, "/logs/query?limit=1&query="+url.QueryEscape(expression), "")
	if response.Code != http.StatusOK {
		t.Fatalf("query status=%d body=%s", response.Code, response.Body.String())
	}
	var result logQueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || !result.Truncated {
		t.Fatalf("query response = %#v", result)
	}
}

func TestLogQueryRejectsInvalidExpressionAndLimit(t *testing.T) {
	router := newTestRouter(t)
	for _, path := range []string{
		"/logs/query",
		"/logs/query?query=" + url.QueryEscape("invalid"),
		"/logs/query?query=" + url.QueryEscape("error{}[1h]") + "&limit=0",
	} {
		response := performRequest(router, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func writeLogForQueryTest(
	t *testing.T,
	router http.Handler,
	timestamp string,
	labels map[string]string,
	message string,
) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"timestamp": timestamp,
		"labels":    labels,
		"message":   message,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(router, http.MethodPost, "/logs", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
}
