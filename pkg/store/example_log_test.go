package store_test

// 本文件演示结构化日志的写入、自动序列号和标签子集查询。

import (
	"fmt"
	"os"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

// ExampleStoreManger_WriteLog 展示面向日志场景的常用写入与查询流程。
func ExampleStoreManger_WriteLog() {
	dir, err := os.MkdirTemp("", "samkv-log-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	options := store.DefaultOptions()
	options.AutoCheckpoint = false
	database, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	sequence, err := database.WriteLog(store.LogEntry{
		Timestamp: at,
		Labels: []utils.Label{
			{Name: "level", Value: "ERROR"},
			{Name: "app", Value: "api"},
		},
		Message: []byte("request failed"),
	})
	results, queryErr := database.Query(at, at, []utils.Label{{Name: "app", Value: "api"}})

	fmt.Println(sequence, err, queryErr, len(results))
	fmt.Println(results[0].Labels, string(results[0].Message))
	// Output:
	// 1 <nil> <nil> 1
	// [{app api} {level ERROR}] request failed
}
