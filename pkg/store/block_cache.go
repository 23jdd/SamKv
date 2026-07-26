package store

// 本文件实现 Store 内所有 SSTable 共享的按字节限制 LRU Block Cache。
// 缓存键包含路径、偏移、大小和格式版本，Compaction 删除文件时会按路径失效条目。

import (
	"container/list"
	"sync"
)

// BlockCacheStats 是 Block Cache 的只读运行统计。
type BlockCacheStats struct {
	// Hits 和 Misses 只统计启用缓存后的内部读取。
	Hits   uint64
	Misses uint64
	// Evictions 是因容量不足发生的 LRU 淘汰次数，显式文件失效不计入。
	Evictions uint64
	// Entries 和 Bytes 是调用 Stats 时的当前占用快照。
	Entries int
	Bytes   int64
}

type blockCacheKey struct {
	path    string
	offset  uint64
	size    uint64
	version uint32
}

type blockCacheEntry struct {
	key  blockCacheKey
	data []byte
}

// BlockCache 是按字节容量限制的并发安全 SSTable Block LRU 缓存。
// 公开零值等价于禁用；缓存内容是完整 block 的副本，内部 get 返回值只允许读取。
type BlockCache struct {
	mu        sync.Mutex
	capacity  int64
	used      int64
	items     map[blockCacheKey]*list.Element
	lru       *list.List
	hits      uint64
	misses    uint64
	evictions uint64
}

// NewBlockCache 创建共享 Block Cache；capacityBytes <= 0 时禁用缓存。
// 大于总容量的单个 block 不会缓存；容量是 payload 字节近似值，不含 map/list 对象开销。
func NewBlockCache(capacityBytes int64) *BlockCache {
	return &BlockCache{
		capacity: capacityBytes,
		items:    make(map[blockCacheKey]*list.Element),
		lru:      list.New(),
	}
}

func (cache *BlockCache) get(key blockCacheKey) ([]byte, bool) {
	if cache == nil || cache.capacity <= 0 {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.items[key]
	if !ok {
		cache.misses++
		return nil, false
	}
	cache.hits++
	cache.lru.MoveToFront(element)
	return element.Value.(*blockCacheEntry).data, true
}

func (cache *BlockCache) put(key blockCacheKey, data []byte) {
	if cache == nil || cache.capacity <= 0 || int64(len(data)) > cache.capacity {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, ok := cache.items[key]; ok {
		entry := element.Value.(*blockCacheEntry)
		cache.used -= int64(len(entry.data))
		entry.data = append(entry.data[:0], data...)
		cache.used += int64(len(entry.data))
		cache.lru.MoveToFront(element)
	} else {
		entry := &blockCacheEntry{key: key, data: append([]byte(nil), data...)}
		cache.items[key] = cache.lru.PushFront(entry)
		cache.used += int64(len(entry.data))
	}
	for cache.used > cache.capacity {
		cache.removeElement(cache.lru.Back())
		cache.evictions++
	}
}

func (cache *BlockCache) removeFile(path string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for element := cache.lru.Back(); element != nil; {
		previous := element.Prev()
		if element.Value.(*blockCacheEntry).key.path == path {
			cache.removeElement(element)
		}
		element = previous
	}
}

func (cache *BlockCache) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*blockCacheEntry)
	delete(cache.items, entry.key)
	cache.used -= int64(len(entry.data))
	cache.lru.Remove(element)
}

// Stats 返回命中、未命中、淘汰数量和当前占用。
// nil 或禁用缓存返回全零快照；累计计数只在进程生命周期内有效。
func (cache *BlockCache) Stats() BlockCacheStats {
	if cache == nil {
		return BlockCacheStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return BlockCacheStats{
		Hits:      cache.hits,
		Misses:    cache.misses,
		Evictions: cache.evictions,
		Entries:   len(cache.items),
		Bytes:     cache.used,
	}
}
