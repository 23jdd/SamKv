package store

import (
	"container/list"
	"sync"
)

// BlockCacheStats 是 Block Cache 的只读运行统计。
type BlockCacheStats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Entries   int
	Bytes     int64
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
