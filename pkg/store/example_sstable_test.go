package store_test

// 本文件展示 SSTable 的唯一写入路径，以及重开后的点查和半开区间扫描。
// WriteSStable 会排序输入；OpenSStable 成功后必须调用 Close。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/store"
)

func ExampleWriteSStable() {
	dir, err := os.MkdirTemp("", "samkv-sstable-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "example.sst")
	created, err := store.WriteSStable(path, []store.Record{
		{Key: "c", Val: "3"},
		{Key: "a", Val: "1"},
		{Key: "b", Val: "2"},
	})
	if err != nil {
		panic(err)
	}
	_ = created.Close()

	table, err := store.OpenSStable(path)
	if err != nil {
		panic(err)
	}
	defer table.Close()

	value, found, err := table.Get("b")
	fmt.Println(value, found, err)

	records, err := table.Scan("b", "d")
	if err != nil {
		panic(err)
	}
	for _, record := range records {
		fmt.Printf("%s=%s ", record.Key, record.Val)
	}

	// Output:
	// 2 true <nil>
	// b=2 c=3
}
