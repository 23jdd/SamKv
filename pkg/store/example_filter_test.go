package store_test

// 本文件展示 BloomFilter 的构造、写入和否定查询。
// true 只表示“可能存在”，业务代码仍必须查询 SSTable 才能确认。

import (
	"fmt"

	"github.com/23jdd/SamKv/pkg/store"
)

func ExampleBloomFilter() {
	filter, err := store.NewBloomFilter(1000, 0.01)
	if err != nil {
		panic(err)
	}
	filter.AddString("known-key")

	fmt.Println(filter.ContainsString("known-key"))
	fmt.Println(filter.ContainsString("definitely-absent-in-this-example"))

	// Output:
	// true
	// false
}
