package store

// 本文件验证全量 Compaction 的版本覆盖、墓碑回收、保留策略、原子发布和错误清理边界。

import (
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

func TestCompactAppliesTimeRetention(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.Retention = 24 * time.Hour

	store, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	labels := []utils.Label{{Name: "app", Value: "nginx"}}

	if _, err := store.WriteLog(LogEntry{
		Timestamp: now.Add(-48 * time.Hour),
		Labels:    labels,
		Message:   []byte("expired"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteLog(LogEntry{
		Timestamp: now.Add(-time.Hour),
		Labels:    labels,
		Message:   []byte("retained"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	result, err := store.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputRecords != 1 {
		t.Fatalf("retention compaction result = %#v", result)
	}
	got, err := store.Query(now.Add(-72*time.Hour), now, labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Message) != "retained" {
		t.Fatalf("retained logs = %#v", got)
	}
}

func TestCompactAppliesSizeRetentionFromOldestLogs(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0

	store, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Unix(1_000, 0).UTC()
	labels := []utils.Label{{Name: "app", Value: "size"}}
	for i := 0; i < 3; i++ {
		if _, err := store.WriteLog(LogEntry{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Labels:    labels,
			Message:   []byte{byte('a' + i)},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}

	latestRecords, err := store.sstables[len(store.sstables)-1].AllRecords()
	if err != nil {
		t.Fatal(err)
	}
	store.options.MaxSizeBytes = approximateSSTableRecordSize(latestRecords[0])

	result, err := store.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputRecords != 1 {
		t.Fatalf("size compaction result = %#v", result)
	}
	got, err := store.Query(base, base.Add(3*time.Second), labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Message) != "c" {
		t.Fatalf("size-retained logs = %#v", got)
	}
}
