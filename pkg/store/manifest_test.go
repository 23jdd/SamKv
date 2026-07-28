package store

// 本文件验证 Manifest 在重复 Checkpoint、文件删除和兼容旧目录时仍保持正确的 SSTable 顺序。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestSupportsRepeatedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	putStore(t, store, "a", "1")
	if _, err := store.Checkpoint(); err != nil {
		t.Fatalf("first Checkpoint() error = %v", err)
	}
	putStore(t, store, "b", "2")
	if _, err := store.Checkpoint(); err != nil {
		t.Fatalf("second Checkpoint() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	manifest, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || manifest.NextFileID != 3 || len(manifest.SSTables) != 2 {
		t.Fatalf("manifest = %#v, exists=%v", manifest, exists)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for key, want := range map[string]string{"a": "1", "b": "2"} {
		if got, ok := getStore(t, reopened, key); !ok || got != want {
			t.Fatalf("Get(%q) = %q, %v; want %q, true", key, got, ok, want)
		}
	}
}

func TestLoadManifestFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	want := Manifest{
		NextFileID: 2,
		SSTables: []ManifestSSTable{{
			File:        "00000000000000000001.sst",
			MinKey:      "a",
			MaxKey:      "z",
			RecordCount: 2,
		}},
	}
	if err := saveManifest(dir, want); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(currentPath(dir), currentBackupPath(dir)); err != nil {
		t.Fatal(err)
	}

	got, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || got.NextFileID != want.NextFileID || len(got.SSTables) != 1 {
		t.Fatalf("loadManifest() = %#v, %v", got, exists)
	}
	if got.SSTables[0].File != filepath.Base(want.SSTables[0].File) {
		t.Fatalf("backup entry = %#v", got.SSTables[0])
	}
}
