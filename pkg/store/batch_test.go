package store

// 本文件覆盖批次内同 key 顺序覆盖、大记录 WAL 编码以及关闭重开后的恢复。
// 用例证明操作顺序稳定，但不把 WriteBatch 描述为支持回滚的数据库事务。

import (
	"strings"
	"testing"
)

func TestWriteBatchPersistsOrderedOperations(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.MemTableLimit = 0
	options.CompactionThreshold = 0

	store, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	batch := NewBatch().
		Put("a", "old").
		Put("b", strings.Repeat("b", 5000)).
		Put("a", "new")
	if err := store.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	if _, ok := getStore(t, store, "a"); ok {
		t.Fatal("a should not exist")
	}
	if value, ok := getStore(t, store, "b"); !ok || len(value) != 5000 {
		t.Fatalf("Get(b) = len:%d, %v", len(value), ok)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok := getStore(t, reopened, "a"); ok {
		t.Fatal("recovered a should not exist")
	}
	if value, ok := getStore(t, reopened, "b"); !ok || len(value) != 5000 {
		t.Fatalf("recovered Get(b) = len:%d, %v", len(value), ok)
	}
}
