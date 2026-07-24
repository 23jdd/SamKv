package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadManifestDefaultsLegacyVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MANIFEST")
	data := []byte(`{
		"next_file_id": 2,
		"last_sequence": 7,
		"sstables": [{
			"file": "00000000000000000001.sst",
			"level": 0,
			"min_key": "a",
			"max_key": "z",
			"record_count": 1
		}]
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != 1 || manifest.SSTables[0].FormatVersion != 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestSaveManifestRejectsFutureVersion(t *testing.T) {
	manifest := newManifest()
	manifest.FormatVersion = CurrentManifestVersion + 1
	if err := saveManifest(t.TempDir(), manifest); err == nil {
		t.Fatal("saveManifest() accepted future format")
	}
}

func TestCheckpointRecordsCurrentSSTableVersion(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Put("key", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if database.manifest.FormatVersion != CurrentManifestVersion ||
		database.manifest.SSTables[0].FormatVersion != currentSSTableVersion {
		t.Fatalf("manifest = %#v", database.manifest)
	}
}
