package main

// 本文件验证支持 GetWithError 的 Store 会把磁盘读取失败映射为 500 而不是 404。

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
