package store

import (
	"sync"
	"testing"
)

func TestBlockCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := NewBlockCache(8)
	a := blockCacheKey{path: "table", offset: 0, size: 4, version: 2}
	b := blockCacheKey{path: "table", offset: 4, size: 4, version: 2}
	c := blockCacheKey{path: "table", offset: 8, size: 4, version: 2}
	cache.put(a, []byte("aaaa"))
	cache.put(b, []byte("bbbb"))
	if _, ok := cache.get(a); !ok {
		t.Fatal("entry a was not cached")
	}
	cache.put(c, []byte("cccc"))
	if _, ok := cache.get(b); ok {
		t.Fatal("least recently used entry b was not evicted")
	}
	stats := cache.Stats()
	if stats.Hits != 1 || stats.Misses != 1 || stats.Evictions != 1 || stats.Entries != 2 || stats.Bytes != 8 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestBlockCacheInvalidatesOneSSTable(t *testing.T) {
	cache := NewBlockCache(32)
	first := blockCacheKey{path: "first.sst", offset: 0}
	second := blockCacheKey{path: "second.sst", offset: 0}
	cache.put(first, []byte("first"))
	cache.put(second, []byte("second"))
	cache.removeFile("first.sst")
	if _, ok := cache.get(first); ok {
		t.Fatal("first table entry survived invalidation")
	}
	if data, ok := cache.get(second); !ok || string(data) != "second" {
		t.Fatalf("second table entry = %q, %v", data, ok)
	}
}

func TestBlockCacheSupportsConcurrentReaders(t *testing.T) {
	cache := NewBlockCache(1024)
	key := blockCacheKey{path: "table.sst", offset: 10}
	cache.put(key, []byte("payload"))
	var wait sync.WaitGroup
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < 100; j++ {
				if data, ok := cache.get(key); !ok || string(data) != "payload" {
					t.Errorf("get = %q, %v", data, ok)
					return
				}
			}
		}()
	}
	wait.Wait()
}
