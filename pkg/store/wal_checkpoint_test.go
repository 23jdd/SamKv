package store

// 本文件验证 MemTable 冻结产生 WAL segment 边界，且 Checkpoint 只回收已发布数据对应的旧段。

import (
	"testing"

	"github.com/23jdd/SamKv/pkg/wal"
)

func TestCheckpointPrunesOnlyFrozenWALSegments(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := database.Put("before", "checkpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	segments, err := wal.ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].ID != 2 || segments[0].Size != 0 {
		t.Fatalf("segments after checkpoint = %+v", segments)
	}

	if err := database.Put("after", "checkpoint"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for key, want := range map[string]string{"before": "checkpoint", "after": "checkpoint"} {
		if got, ok := reopened.Get(key); !ok || got != want {
			t.Fatalf("Get(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
}

func TestMemTableCarriesSealedSegmentBoundary(t *testing.T) {
	database, err := NewStoreManager(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Put("key", "value"); err != nil {
		t.Fatal(err)
	}

	database.mu.Lock()
	frozen, err := database.freezeActiveLocked()
	database.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !frozen || len(database.immutables) != 1 {
		t.Fatalf("frozen = %v, immutables = %d", frozen, len(database.immutables))
	}
	if cutoff := database.immutables[0].WALSegmentCutoff(); cutoff != 1 {
		t.Fatalf("WALSegmentCutoff = %d, want 1", cutoff)
	}
}
