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
	tp := &TieredPool{
		caps:  capacities,
		pools: make([]sync.Pool, len(capacities)),
	}
	for i, c := range capacities {
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
	for i, c := range tp.caps {
		if c >= size {
			buf := tp.pools[i].Get().([]byte)
			if cap(buf) < size {
				// Should never happen under normal use, but be defensive.
				tp.pools[i].Put(buf[:0])
				return make([]byte, size)
			}
			return buf[:size]
		}
	}
	return make([]byte, size)
}

// Put 归还缓冲：按容量放回对应桶；超过最大桶容量则丢弃。
// Put returns a buffer to the pool. The buffer's capacity determines which
// pool it goes into: it's placed into the smallest pool whose capacity >= cap(buf).
// If the capacity exceeds the largest pool's capacity, the buffer is discarded.
func (tp *TieredPool) Put(buf []byte) {
	c := cap(buf)
	for i, capa := range tp.caps {
		if c <= capa {
			tp.pools[i].Put(buf[:0])
			return
		}
	}
	// Discard: capacity too large.
}