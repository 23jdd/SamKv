package store_test

// 本文件演示 Checkpoint 如何把内存数据发布为 SSTable，并继续通过普通 Get 读取。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/store"
)

// ExampleStoreManger_Checkpoint 展示手动持久化的最小流程。
// 使用 WALSyncAlways 时 Put 返回前 WAL 已 fsync；Checkpoint 进一步把数据转成 SSTable 并更新 MANIFEST。
func ExampleStoreManger_Checkpoint() {
	dir, err := os.MkdirTemp("", "samkv-checkpoint-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	options := store.DefaultOptions()
	options.AutoCheckpoint = false
	options.WALSyncPolicy = store.WALSyncEveryWrite
	database, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	if err := database.WriteBatch(store.NewBatch().Put("service", "api")); err != nil {
		panic(err)
	}
	path, err := database.Checkpoint()
	records, _ := database.Scan("service", "service\x00")
	value, found := "", false
	if len(records) > 0 && !records[0].Deleted {
		value, found = records[0].Val, true
	}
	fmt.Println(filepath.Base(path), err)
	fmt.Println(value, found)
	// Output:
	// 00000000000000000001.sst <nil>
	// api true
}
