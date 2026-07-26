package store_test

// 本文件展示独立 MemTable 和 Store Batch 的基本用法。
// Example 不依赖内部字段，代码可以直接迁移到外部调用方。

import (
	"fmt"
	"os"

	"github.com/23jdd/SamKv/pkg/store"
)

func ExampleMemTable() {
	mem := store.NewMemTable(0)
	_ = mem.Put("b", "2")
	_ = mem.Put("a", "1")
	_ = mem.Delete("b")

	for _, record := range mem.Entries() {
		fmt.Println(record.Key, record.Val, record.Deleted)
	}

	// Output:
	// a 1 false
	// b  true
}

func ExampleBatch() {
	dir, err := os.MkdirTemp("", "samkv-batch-example-")
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

	batch := store.NewBatch().
		Put("status", "starting").
		Put("status", "ready").
		Delete("obsolete")
	if err := database.WriteBatch(batch); err != nil {
		panic(err)
	}
	value, found := database.Get("status")
	fmt.Println(value, found)

	// Output:
	// ready true
}
