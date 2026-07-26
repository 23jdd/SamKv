package store

// 本文件验证分层选择策略、层容量触发、Manifest 更新以及墓碑只在末层回收的规则。

import (
	"path/filepath"
	"testing"
)

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

func TestCompactLevelPublishesParallelOutputs(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.CompactionWorkers = 3
	options.CompactionTaskBytes = 1
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}

	keys := []string{"a", "c", "e", "g", "i", "k"}
	for _, key := range keys {
		if err := database.Put(key, "value-"+key); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}

	result, err := database.CompactLevel(0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Subtasks != 3 || result.OutputTables != 3 || len(result.Paths) != 3 {
		t.Fatalf("parallel compaction result = %#v", result)
	}
	if result.Path != result.Paths[0] || result.InputTables != len(keys) || result.OutputRecords != len(keys) {
		t.Fatalf("compaction compatibility fields = %#v", result)
	}
	if database.manifest.NextFileID != 10 || countLevel(database.manifest, 0) != 0 || countLevel(database.manifest, 1) != 3 {
		t.Fatalf("parallel manifest = %#v", database.manifest)
	}
	for index := 1; index < len(database.manifest.SSTables); index++ {
		previous := database.manifest.SSTables[index-1]
		current := database.manifest.SSTables[index]
		if previous.MaxKey >= current.MinKey {
			t.Fatalf("output ranges overlap: %#v and %#v", previous, current)
		}
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != result.OutputTables {
		t.Fatalf("SSTable files = %d, want %d", len(paths), result.OutputTables)
	}
	stats := database.Stats()
	if stats.CompactionSubtasks != 3 || stats.CompactionOutputFiles != 3 {
		t.Fatalf("parallel compaction stats = %#v", stats)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, key := range keys {
		if value, ok := reopened.Get(key); !ok || value != "value-"+key {
			t.Fatalf("Get(%q) = %q, %v after reopen", key, value, ok)
		}
	}
}
