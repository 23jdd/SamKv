package store

// 本文件验证旧固定名 MANIFEST/MANIFEST.bak 在读取成功后迁移到 CURRENT 世代协议。

import (
	"encoding/json"
	"os"
	"testing"
)

func writeLegacyManifestForMigration(t testing.TB, path string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifestMigratesLegacyPrimary(t *testing.T) {
	dir := t.TempDir()
	legacy := newManifest()
	legacy.NextFileID = 7
	writeLegacyManifestForMigration(t, manifestPath(dir), legacy)

	got, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || got.NextFileID != 7 {
		t.Fatalf("loadManifest() = %+v, %v", got, exists)
	}
	if _, err := os.Stat(currentPath(dir)); err != nil {
		t.Fatalf("CURRENT missing after migration: %v", err)
	}
	if _, err := os.Stat(manifestPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("legacy MANIFEST still exists: %v", err)
	}
}

func TestLoadManifestMigratesLegacyBackup(t *testing.T) {
	dir := t.TempDir()
	legacy := newManifest()
	legacy.NextFileID = 9
	writeLegacyManifestForMigration(t, manifestBackupPath(dir), legacy)

	got, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || got.NextFileID != 9 {
		t.Fatalf("loadManifest() = %+v, %v", got, exists)
	}
	if _, err := os.Stat(currentPath(dir)); err != nil {
		t.Fatalf("CURRENT missing after backup migration: %v", err)
	}
	if _, err := os.Stat(manifestBackupPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("legacy backup still exists: %v", err)
	}
}
