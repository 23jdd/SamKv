package main

import (
	"errors"
	"net/http"
	"testing"
)

func TestKVRouterReturnsServerErrorForSSTableReadFailure(t *testing.T) {
	database := &readErrorStore{err: errors.New("checksum mismatch")}
	response := performRequest(NewRouter(database), http.MethodGet, "/kv/key", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
}

type readErrorStore struct {
	stubKVStore
	err error
}

func (store *readErrorStore) GetWithError(string) (string, bool, error) {
	return "", false, store.err
}
