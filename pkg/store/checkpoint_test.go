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

func TestStoreLoadsSSTablesFromManifest(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}

	if err := st.Put("persisted", "value"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
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

	value, ok := reopened.Get("persisted")
	if !ok || value != "value" {
		t.Fatalf("Get(persisted) after reopen = %q, %v; want value, true", value, ok)
	}
	if reopened.nextSSTableID != 2 {
		t.Fatalf("reopened nextSSTableID = %d, want 2", reopened.nextSSTableID)
	}
}
