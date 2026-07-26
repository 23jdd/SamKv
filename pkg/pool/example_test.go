package pool

// 本文件提供可由 go test 校验的分级缓冲池用法示例。
// 示例只观察长度和容量，不依赖 sync.Pool 是否返回同一个底层数组。

import (
	"fmt"
)

func ExampleTieredPool() {
	pool := NewTieredPool(8, 16, 64)
	buffer := pool.Get(12)
	fmt.Println(len(buffer), cap(buffer))

	// Put 后不得继续使用 buffer；sync.Pool 也不保证下一次一定复用它。
	pool.Put(buffer)

	oversized := pool.Get(100)
	fmt.Println(len(oversized), cap(oversized))

	// Output:
	// 12 16
	// 100 100
}
