package skiplist_test

// 本文件展示跳表的插入、覆盖、下界查询和有序快照。
// Example 的输出由 go test 校验，避免文档与实际排序语义漂移。

import (
	"fmt"

	skiplist "github.com/23jdd/SamKv/pkg/skipList"
)

func ExampleSkipList() {
	list := skiplist.New[int, string](func(a, b int) int { return a - b })
	list.Add(20, "twenty")
	list.Add(10, "ten")
	old, replaced := list.Set(20, "TWENTY")
	fmt.Println(old, replaced)

	key, value, found := list.LowerBound(15)
	fmt.Println(key, value, found)
	for _, entry := range list.Entries() {
		fmt.Printf("%d=%s ", entry.Key, entry.Value)
	}

	// Output:
	// twenty true
	// 20 TWENTY true
	// 10=ten 20=TWENTY
}
