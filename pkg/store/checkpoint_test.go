package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCheckpointWritesSSTableAndResetsWAL(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	defer st.Close()

	if err := st.Put("a", "1"); err != nil {
		t.Fatalf("Put(a) error = %v", err)
	}
	if err := st.Put("b", "2"); err != nil {
		t.Fatalf("Put(b) error = %v", err)
	}

	path, err := st.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
	if path == "" {
		t.Fatal("Checkpoint() returned empty SSTable path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint SSTable stat error = %v", err)
	}
	if st.mem.Len() != 0 || st.mem.Size() != 0 {
		t.Fatalf("memtable after checkpoint len=%d size=%d, want 0/0", st.mem.Len(), st.mem.Size())
	}

	value, ok := st.Get("a")
	if !ok || value != "1" {
		t.Fatalf("Get(a) after checkpoint = %q, %v; want 1, true", value, ok)
	}

	walInfo, err := os.Stat(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatalf("wal stat error = %v", err)
	}
	if walInfo.Size() != 0 {
		t.Fatalf("wal size after checkpoint = %d, want 0", walInfo.Size())
	}
}

func TestStoreCheckpointKeepsTombstoneAboveOlderSSTable(t *testing.T) {
	st, err := NewStoreManger(t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	defer st.Close()

	if err := st.Put("k", "old"); err != nil {
		t.Fatalf("Put(old) error = %v", err)
	}
	if _, err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint(old) error = %v", err)
	}

	if err := st.Delete("k"); err != nil {
		t.Fatalf("Delete(k) error = %v", err)
	}
	if _, err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint(tombstone) error = %v", err)
	}

	if value, ok := st.Get("k"); ok {
		t.Fatalf("Get(k) after tombstone checkpoint = %q, true; want false", value)
	}
}
