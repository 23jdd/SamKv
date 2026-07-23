package tcp

import "sync"

// TieredPool 分级缓冲池：按不同容量分桶复用 []byte，减少内存浪费与分配。
// TieredPool is a collection of sync.Pools of different capacities,
// designed to reuse []byte slices of varying sizes while minimizing memory waste.
type TieredPool struct {
	caps  []int
	pools []sync.Pool
}

// NewTieredPool 按给定（升序）容量列表创建分级缓冲池。
// NewTieredPool New creates a new TieredPool with the given capacities.
// Each capacity defines a pool of buffers with that exact capacity.
// The capacities slice must be sorted in ascending order.
func NewTieredPool(capacities ...int) *TieredPool {
	if len(capacities) == 0 {
		panic("tiered buffer: capacities must not be empty")
	}
	caps := append([]int(nil), capacities...)
	for i, capacity := range caps {
		if capacity <= 0 {
			panic("tiered buffer: capacities must be positive")
		}
		if i > 0 && capacity <= caps[i-1] {
			panic("tiered buffer: capacities must be strictly increasing")
		}
	}
	tp := &TieredPool{
		caps:  caps,
		pools: make([]sync.Pool, len(caps)),
	}
	for i, c := range caps {
		c := c
		tp.pools[i].New = func() any {
			return make([]byte, 0, c)
		}
	}
	return tp
}

// Get 取出一个长度为 size、容量不小于 size 的缓冲（从能容纳的最小桶取）。
// Get returns a []byte of length size with capacity at least size.
// The buffer is taken from the smallest pool whose capacity >= size.
// If no pool is large enough, a new buffer is allocated without pooling.
func (tp *TieredPool) Get(size int) []byte {
	if size < 0 {
		panic("tiered buffer: size must not be negative")
	}
	for i, c := range tp.caps {
		if c >= size {
			buf := tp.pools[i].Get().([]byte)
			if cap(buf) < size {
				// 防御外部错误归还的缓冲，不能再次放回并持续污染当前桶。
				return make([]byte, size)
			}
			return buf[:size]
		}
	}
	return make([]byte, size)
}

// Put 归还缓冲：只有容量与某个桶完全匹配时才复用，其他缓冲直接丢弃。
// 精确匹配可以保证桶内缓冲始终满足该桶的容量约束。
func (tp *TieredPool) Put(buf []byte) {
	c := cap(buf)
	for i, capacity := range tp.caps {
		if c == capacity {
			tp.pools[i].Put(buf[:0])
			return
		}
		if c < capacity {
			return
		}
	}
}
