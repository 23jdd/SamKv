package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeFormatRewritesLegacySSTable(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "00000000000000000001.sst")
	if err := writeLegacySStableForTest(legacyPath, []Record{{Key: "legacy", Val: "value"}}); err != nil {
		t.Fatal(err)
	}
	legacyTable, err := OpenSStable(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifestEntryFromSSTable(legacyPath, legacyTable)
	if err := legacyTable.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := newManifest()
	manifest.NextFileID = 2
	manifest.SSTables = []ManifestSSTable{entry}
	if err := saveManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}

	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if database.sstables[0].Version() != legacySSTableVersion {
		t.Fatalf("opened version = %d", database.sstables[0].Version())
	}
	result, err := database.UpgradeFormat()
	if err != nil {
		t.Fatal(err)
	}
	if result.RewrittenTables != 1 || result.SSTableVersion != currentSSTableVersion || result.OutputPath == "" {
		t.Fatalf("upgrade result = %#v", result)
	}
	if len(database.sstables) != 1 || database.sstables[0].Version() != currentSSTableVersion {
		t.Fatalf("upgraded tables = %#v", database.sstables)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy file still exists: %v", err)
	}
	if value, ok := database.Get("legacy"); !ok || value != "value" {
		t.Fatalf("Get(legacy) = %q, %v", value, ok)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.manifest.SSTables[0].FormatVersion != currentSSTableVersion {
		t.Fatalf("manifest = %#v", reopened.manifest)
	}
}
