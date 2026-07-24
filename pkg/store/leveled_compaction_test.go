package store

import "testing"

func TestCompactLevelPreservesTombstoneUntilBottomLevel(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.MaxLevels = 3
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := database.Put("key", "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := database.Delete("key"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	result, err := database.CompactLevel(0)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceLevel != 0 || result.TargetLevel != 1 || result.InputTables != 2 {
		t.Fatalf("L0 result = %#v", result)
	}
	records, err := database.sstables[0].AllRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].Deleted {
		t.Fatalf("L1 records = %#v, want one tombstone", records)
	}

	result, err = database.CompactLevel(1)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetLevel != 2 || result.OutputRecords != 0 || len(database.sstables) != 0 {
		t.Fatalf("L1 result = %#v, tables=%d", result, len(database.sstables))
	}
	if _, ok := database.Get("key"); ok {
		t.Fatal("deleted key was resurrected")
	}
}

func TestCompactLevelOnlyMovesOneNonZeroLevelTable(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.MaxLevels = 4
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, key := range []string{"a", "z"} {
		if err := database.Put(key, key); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompactLevel(0); err != nil {
			t.Fatal(err)
		}
	}
	if countLevel(database.manifest, 1) != 2 {
		t.Fatalf("manifest before L1 compaction = %#v", database.manifest)
	}

	result, err := database.CompactLevel(1)
	if err != nil {
		t.Fatal(err)
	}
	if result.InputTables != 1 || result.SourceLevel != 1 || result.TargetLevel != 2 {
		t.Fatalf("L1 result = %#v", result)
	}
	if countLevel(database.manifest, 1) != 1 || countLevel(database.manifest, 2) != 1 {
		t.Fatalf("manifest after L1 compaction = %#v", database.manifest)
	}
	for _, key := range []string{"a", "z"} {
		if value, ok := database.Get(key); !ok || value != key {
			t.Fatalf("Get(%s) = %q, %v", key, value, ok)
		}
	}
}

func countLevel(manifest Manifest, level int) int {
	count := 0
	for _, entry := range manifest.SSTables {
		if entry.Level == level {
			count++
		}
	}
	return count
}
