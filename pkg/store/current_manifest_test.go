package store

// 本文件验证 CURRENT 指向最新 Manifest、世代保留数量和损坏指针拒绝边界。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveManifestPublishesCurrentGeneration(t *testing.T) {
	dir := t.TempDir()
	first := newManifest()
	first.NextFileID = 2
	if err := saveManifest(dir, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.NextFileID = 3
	if err := saveManifest(dir, second); err != nil {
		t.Fatal(err)
	}

	currentData, err := os.ReadFile(currentPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimSpace(string(currentData))
	if name != manifestGenerationName(2) {
		t.Fatalf("CURRENT = %q", name)
	}
	manifest, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || manifest.NextFileID != 3 {
		t.Fatalf("loadManifest() = %+v, %v", manifest, exists)
	}
	generations, err := listManifestGenerations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 2 {
		t.Fatalf("generation count = %d, want 2", len(generations))
	}
}

func TestLoadManifestRejectsUnsafeCurrentTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(currentPath(dir), []byte("../MANIFEST-00000000000000000001\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadManifest(dir); err == nil {
		t.Fatal("loadManifest() accepted unsafe CURRENT target")
	}
}

func TestUnreferencedManifestGenerationIsNotSelected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(manifestGenerationPath(dir, 1), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	path, exists, err := activeManifestPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if exists || path != "" {
		t.Fatalf("activeManifestPath() = %q, %v", filepath.Base(path), exists)
	}
}
