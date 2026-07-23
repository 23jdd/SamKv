package store

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
		Put("a", "new").
		Delete("a")
	if err := store.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("a should be hidden by the batch tombstone")
	}
	if value, ok := store.Get("b"); !ok || len(value) != 5000 {
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
	if _, ok := reopened.Get("a"); ok {
		t.Fatal("recovered a should remain deleted")
	}
	if value, ok := reopened.Get("b"); !ok || len(value) != 5000 {
		t.Fatalf("recovered Get(b) = len:%d, %v", len(value), ok)
	}
}
