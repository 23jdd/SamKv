package store

// 本文件验证不同 WALSyncPolicy 的 fsync 时机和关闭前刷盘行为。

import (
	"errors"
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
	putStore(t, database, "durable", "value")

	recovered := NewMemTable(0)
	if _, err := RecoverWALDirectory(database.dir, recovered); err != nil {
		t.Fatal(err)
	}
	if entry, ok := recovered.table.Get("durable"); !ok || entry.Deleted || entry.Value != "value" {
		t.Fatalf("recovered value = %#v, %v", entry, ok)
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
