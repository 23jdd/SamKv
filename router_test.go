package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/gin-gonic/gin"
)

func TestKVRouterPutGetDelete(t *testing.T) {
	router := newTestRouter(t)

	response := performRequest(router, http.MethodPut, "/kv/services/api", `{"value":"ready"}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}

	response = performRequest(router, http.MethodGet, "/kv/services/api", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	var got kvResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Key != "services/api" || got.Value != "ready" {
		t.Fatalf("GET response=%#v", got)
	}

	response = performRequest(router, http.MethodDelete, "/kv/services/api", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", response.Code, response.Body.String())
	}

	response = performRequest(router, http.MethodGet, "/kv/services/api", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET deleted key status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKVRouterAllowsEmptyValue(t *testing.T) {
	router := newTestRouter(t)

	response := performRequest(router, http.MethodPut, "/kv/empty", `{"value":""}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}
	response = performRequest(router, http.MethodGet, "/kv/empty", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	var got kvResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Value != "" {
		t.Fatalf("GET value=%q, want empty", got.Value)
	}
}

func TestKVRouterRejectsInvalidRequests(t *testing.T) {
	router := newTestRouter(t)
	tests := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{name: "missing key", path: "/kv", body: `{"value":"x"}`, status: http.StatusBadRequest},
		{name: "missing value", path: "/kv/key", body: `{}`, status: http.StatusBadRequest},
		{name: "unknown field", path: "/kv/key", body: `{"value":"x","extra":true}`, status: http.StatusBadRequest},
		{name: "malformed JSON", path: "/kv/key", body: `{"value":`, status: http.StatusBadRequest},
		{name: "multiple objects", path: "/kv/key", body: `{"value":"x"}{"value":"y"}`, status: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(router, http.MethodPut, test.path, test.body)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

func TestHealthReportsBackgroundFailure(t *testing.T) {
	database := &stubKVStore{backgroundErr: errors.New("flush failed")}
	router := NewRouter(database)

	response := performRequest(router, http.MethodGet, "/healthz", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
}

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	options := store.DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := store.NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return NewRouter(database)
}

func performRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type stubKVStore struct {
	backgroundErr error
}

func (s *stubKVStore) Put(string, string) error  { return nil }
func (s *stubKVStore) Get(string) (string, bool) { return "", false }
func (s *stubKVStore) Delete(string) error       { return nil }
func (s *stubKVStore) BackgroundError() error    { return s.backgroundErr }
