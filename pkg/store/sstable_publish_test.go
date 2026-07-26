package store

// 本文件覆盖 SSTable 发布中断后的恢复边界：临时文件、孤立文件和目标文件覆盖。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSStableRefusesToOverwritePublishedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "00000000000000000001.sst")
	if _, err := WriteSStable(path, []Record{{Key: "key", Val: "old"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSStable(path, []Record{{Key: "key", Val: "new"}}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second WriteSStable() error = %v, want os.ErrExist", err)
	}
	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	if got, ok, err := table.Get("key"); err != nil || !ok || got != "old" {
		t.Fatalf("Get(key) = %q, %v, %v; want old, true, nil", got, ok, err)
	}
}

func TestStoreIgnoresOrphanSStableAndReservesItsID(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put("committed", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	orphanPath := sstablePath(dir, 99)
	if _, err := WriteSStable(orphanPath, []Record{{Key: "orphan", Val: "hidden"}}); err != nil {
		t.Fatal(err)
	}
	staleTemp := filepath.Join(dir, "00000000000000000100.sst.tmp")
	if err := os.WriteFile(staleTemp, []byte("incomplete"), 0644); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(staleTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp stat error = %v, want os.ErrNotExist", err)
	}
	if got, ok := reopened.Get("orphan"); ok || got != "" {
		t.Fatalf("Get(orphan) = %q, %v; want empty, false", got, ok)
	}
	if reopened.nextSSTableID != 100 {
		t.Fatalf("nextSSTableID = %d, want 100", reopened.nextSSTableID)
	}
	if len(reopened.sstables) != 1 {
		t.Fatalf("loaded SSTables = %d, want only the manifest table", len(reopened.sstables))
	}
}
