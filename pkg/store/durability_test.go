package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreStrictDurabilityWritesWALBeforePutReturns(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.WALSyncPolicy = WALSyncEveryWrite
	options.WALSyncInterval = 0

	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Put("durable", "value"); err != nil {
		t.Fatal(err)
	}

	recovered := NewMemTable(0)
	if err := RecoverWALFile(filepath.Join(database.dir, "wal.log"), recovered); err != nil {
		t.Fatal(err)
	}
	if value, ok := recovered.Get("durable"); !ok || value != "value" {
		t.Fatalf("recovered value = %q, %v", value, ok)
	}
}

func TestStoreRejectsInvalidWALOptions(t *testing.T) {
	tests := []Options{
		func() Options {
			options := DefaultOptions()
			options.WALSyncPolicy = WALSyncPolicy(99)
			return options
		}(),
		func() Options {
			options := DefaultOptions()
			options.WALSyncInterval = -time.Second
			return options
		}(),
	}
	for _, options := range tests {
		if _, err := NewStoreManagerWithOptions(t.TempDir(), options); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("NewStoreManagerWithOptions() error = %v, want ErrInvalidOptions", err)
		}
	}
}
