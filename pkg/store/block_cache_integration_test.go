package store

// 本文件覆盖 Store 重开后的真实 Block Cache 命中，以及 Verify 绕过缓存读取磁盘。
// 后者保证缓存中的旧副本不能掩盖底层文件发生的校验损坏。

import (
	"errors"
	"testing"
)

func TestStoreReadsSSTableThroughSharedBlockCache(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.BlockCacheBytes = 1 << 20

	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	putStore(t, database, "cached", "value")
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for i := 0; i < 2; i++ {
		if value, ok := getStore(t, database, "cached"); !ok || value != "value" {
			t.Fatalf("Get(cached) = %q, %v", value, ok)
		}
	}
	stats := database.Stats().BlockCache
	if stats.Misses != 1 || stats.Hits != 1 || stats.Entries != 1 {
		t.Fatalf("block cache stats = %#v", stats)
	}
}

func TestStoreVerifyBypassesCachedBlock(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	putStore(t, database, "key", "value")
	path, err := database.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, ok := getStore(t, database, "key"); !ok {
		t.Fatal("key not found before corruption")
	}
	handle := database.sstables[0].Index()[0].Handle
	flipFileByte(t, path, int64(handle.Offset))
	if _, err := database.Verify(); !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("Verify() error = %v, want ErrBlockChecksum", err)
	}
}
