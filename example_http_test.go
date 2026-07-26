package main

// 本文件使用 httptest 演示无需监听端口即可调用 KV HTTP 路由。

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type exampleKVStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func (store *exampleKVStore) Put(key, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.data[key] = value
	return nil
}

func (store *exampleKVStore) Get(key string) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, found := store.data[key]
	return value, found
}

func (store *exampleKVStore) Delete(key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.data, key)
	return nil
}

func (store *exampleKVStore) BackgroundError() error { return nil }

// ExampleNewRouter 展示写入和读取普通 KV 的 HTTP 请求格式。
func ExampleNewRouter() {
	gin.SetMode(gin.TestMode)
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	defer func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
	}()

	router := NewRouter(&exampleKVStore{data: make(map[string]string)})

	put := httptest.NewRequest(http.MethodPut, "/kv/greeting", strings.NewReader(`{"value":"hello"}`))
	put.Header.Set("Content-Type", "application/json")
	putResponse := httptest.NewRecorder()
	router.ServeHTTP(putResponse, put)

	get := httptest.NewRequest(http.MethodGet, "/kv/greeting", nil)
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, get)

	fmt.Println(putResponse.Code)
	fmt.Println(getResponse.Code, strings.TrimSpace(getResponse.Body.String()))
	// Output:
	// 204
	// 200 {"key":"greeting","value":"hello"}
}
