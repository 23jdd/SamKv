package store

// 本文件验证 Checkpoint 的 SSTable 发布、WAL 重写、空表处理和故障恢复顺序。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/23jdd/SamKv/pkg/wal"
)

func TestStoreCheckpointWritesSSTableAndResetsWAL(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	defer st.Close()

	putStore(t, st, "a", "1")
	putStore(t, st, "b", "2")

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
	manifest, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}
	if !exists {
		t.Fatal("checkpoint did not create MANIFEST")
	}
	if manifest.NextFileID != 2 {
		t.Fatalf("manifest next file id = %d, want 2", manifest.NextFileID)
	}
	if len(manifest.SSTables) != 1 {
		t.Fatalf("manifest sstable count = %d, want 1", len(manifest.SSTables))
	}
	if manifest.SSTables[0].File != filepath.Base(path) {
		t.Fatalf("manifest sstable file = %q, want %q", manifest.SSTables[0].File, filepath.Base(path))
	}
	if st.mem.Len() != 0 || st.mem.Size() != 0 {
		t.Fatalf("memtable after checkpoint len=%d size=%d, want 0/0", st.mem.Len(), st.mem.Size())
	}

	value, ok := getStore(t, st, "a")
	if !ok || value != "1" {
		t.Fatalf("Get(a) after checkpoint = %q, %v; want 1, true", value, ok)
	}

	segments, err := wal.ListSegments(dir)
	if err != nil {
		t.Fatalf("list WAL segments: %v", err)
	}
	var walBytes int64
	for _, segment := range segments {
		walBytes += segment.Size
	}
	if walBytes != 0 {
		t.Fatalf("WAL bytes after checkpoint = %d, want 0", walBytes)
	}
}

func TestStoreCheckpointKeepsTombstoneAboveOlderSSTable(t *testing.T) {
	st, err := NewStoreManger(t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	defer st.Close()

	putStore(t, st, "k", "old")
	if _, err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint(old) error = %v", err)
	}

	// TODO: KV delete removed in logs-only mode
	// if err := st.Delete("k"); err != nil {
	// 	t.Fatalf("Delete(k) error = %v", err)
	// }
	if _, err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint(tombstone) error = %v", err)
	}

	if value, ok := getStore(t, st, "k"); ok {
		t.Fatalf("Get(k) after tombstone checkpoint = %q, true; want false", value)
	}
}

func TestStoreLoadsSSTablesFromManifest(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}

	putStore(t, st, "persisted", "value")
	if _, err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("reopen NewStoreManger() error = %v", err)
	}
	defer reopened.Close()

	value, ok := getStore(t, reopened, "persisted")
	if !ok || value != "value" {
		t.Fatalf("Get(persisted) after reopen = %q, %v; want value, true", value, ok)
	}
	if reopened.nextSSTableID != 2 {
		t.Fatalf("reopened nextSSTableID = %d, want 2", reopened.nextSSTableID)
	}
}
