package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/gin-gonic/gin"
)

func TestHealthReportsBackgroundFailure(t *testing.T) {
	database := &stubHealthStore{backgroundErr: errors.New("flush failed")}
	router := NewRouter(database)

	response := performRequest(router, http.MethodGet, "/healthz", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHealthReportsOK(t *testing.T) {
	database := &stubHealthStore{}
	router := NewRouter(database)

	response := performRequest(router, http.MethodGet, "/healthz", "")
	if response.Code != http.StatusOK {
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

type stubHealthStore struct {
	backgroundErr error
}

func (s *stubHealthStore) BackgroundError() error { return s.backgroundErr }
func (s *stubHealthStore) WriteLog(store.LogEntry) (uint64, error) { return 0, nil }
func (s *stubHealthStore) WriteLogs([]store.LogEntry) ([]uint64, error) { return nil, nil }
func (s *stubHealthStore) Query(_, _ any, _ any) ([]store.LogEntry, error) { return nil, nil }
func (s *stubHealthStore) Stats() store.Stats { return store.Stats{} }
