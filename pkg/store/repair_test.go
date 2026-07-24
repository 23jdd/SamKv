package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepairDirectoryQuarantinesCorruptionWithoutRevivingOrphans(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put("a", "valid"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Put("b", "corrupt"); err != nil {
		t.Fatal(err)
	}
	corruptPath, err := database.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	corruptTable, err := OpenSStable(corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	handle := corruptTable.Index()[0].Handle
	if err := corruptTable.Close(); err != nil {
		t.Fatal(err)
	}
	flipFileByte(t, corruptPath, int64(handle.Offset))

	orphanPath := filepath.Join(dir, "00000000000000000999.sst")
	if _, err := WriteSStable(orphanPath, []Record{{Key: "orphan", Val: "must-not-return"}}); err != nil {
		t.Fatal(err)
	}

	report, err := RepairDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.ValidTables != 1 || len(report.Quarantined) != 1 || len(report.IgnoredOrphanFiles) != 1 {
		t.Fatalf("repair report = %#v", report)
	}
	if _, err := os.Stat(report.Quarantined[0]); err != nil {
		t.Fatalf("quarantined file: %v", err)
	}
	if _, err := os.Stat(manifestPath(dir) + ".repair.bak"); err != nil {
		t.Fatalf("repair manifest backup: %v", err)
	}

	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if value, ok := reopened.Get("a"); !ok || value != "valid" {
		t.Fatalf("Get(a) = %q, %v", value, ok)
	}
	if _, ok := reopened.Get("b"); ok {
		t.Fatal("corrupted key b was not removed")
	}
	if _, ok := reopened.Get("orphan"); ok {
		t.Fatal("orphan SSTable was unexpectedly added to MANIFEST")
	}
}

func TestRepairDirectoryRefusesOpenStore(t *testing.T) {
	dir := t.TempDir()
	database, err := NewStoreManager(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := RepairDirectory(dir); err == nil {
		t.Fatal("RepairDirectory() succeeded while Store was open")
	}
}
func TestRepairDirectorySupportsBackupOnlyManifest(t *testing.T) {
	dir := t.TempDir()
	database, err := NewStoreManager(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put("key", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(manifestPath(dir), manifestBackupPath(dir)); err != nil {
		t.Fatal(err)
	}

	report, err := RepairDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.ValidTables != 1 {
		t.Fatalf("repair report = %#v", report)
	}
	if _, err := os.Stat(manifestPath(dir)); err != nil {
		t.Fatalf("repaired MANIFEST: %v", err)
	}
	reopened, err := NewStoreManager(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if value, ok := reopened.Get("key"); !ok || value != "value" {
		t.Fatalf("Get(key) = %q, %v", value, ok)
	}
}
