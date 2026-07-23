package store

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

func TestCompactMergesVersionsDropsTombstonesAndDeletesInputs(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0

	store, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Put("a", "old-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("b", "old-b"); err != nil {
		t.Fatal(err)
	}
	firstPath, err := store.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("b", "new-b"); err != nil {
		t.Fatal(err)
	}
	secondPath, err := store.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if result.InputTables != 2 || result.OutputRecords != 1 {
		t.Fatalf("Compact() result = %#v", result)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("a should stay deleted after compaction")
	}
	if value, ok := store.Get("b"); !ok || value != "new-b" {
		t.Fatalf("Get(b) = %q, %v", value, ok)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old SSTable %q still exists, error=%v", path, err)
		}
	}

	manifest, exists, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || len(manifest.SSTables) != 1 || manifest.SSTables[0].Level != 1 {
		t.Fatalf("compacted manifest = %#v", manifest)
	}
}

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
