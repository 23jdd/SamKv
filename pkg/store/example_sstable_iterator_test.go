package store_test

// 本文件展示如何用懒加载迭代器遍历大 SSTable 的半开 key 区间。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/store"
)

func ExampleSSTableIterator() {
	dir, err := os.MkdirTemp("", "samkv-iterator-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "logs.sst")
	if _, err := store.WriteSStable(path, []store.Record{
		{Key: "2026-01", Val: "first"},
		{Key: "2026-02", Deleted: true},
		{Key: "2026-03", Val: "third"},
	}); err != nil {
		panic(err)
	}
	table, err := store.OpenSStable(path)
	if err != nil {
		panic(err)
	}
	defer table.Close()

	iterator, err := table.NewIterator("2026-02", "2026-04")
	if err != nil {
		panic(err)
	}
	defer iterator.Close()
	for iterator.Valid() {
		record := iterator.Record()
		fmt.Println(record.Key, record.Val, record.Deleted)
		iterator.Next()
	}
	if err := iterator.Error(); err != nil {
		panic(err)
	}

	// Output:
	// 2026-02  true
	// 2026-03 third false
}
