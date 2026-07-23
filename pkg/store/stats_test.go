package store

import "testing"

func TestStatsReportsOperationsAndStorage(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	store, err := NewStoreMangerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Put("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("b"); err != nil {
		t.Fatal(err)
	}
	store.Get("a")
	if _, err := store.Scan("", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Compact(); err != nil {
		t.Fatal(err)
	}

	stats := store.Stats()
	if stats.WriteOperations != 2 || stats.ReadOperations != 2 {
		t.Fatalf("operation stats = writes:%d reads:%d", stats.WriteOperations, stats.ReadOperations)
	}
	if stats.Checkpoints != 1 || stats.Compactions != 1 {
		t.Fatalf("maintenance stats = checkpoints:%d compactions:%d", stats.Checkpoints, stats.Compactions)
	}
	if stats.SSTables != 1 || stats.SSTableRecords != 2 || stats.LevelTables[0] != 1 {
		t.Fatalf("sstable stats = %#v", stats)
	}
	if stats.WALBytes != 0 || stats.SSTableBytes == 0 || stats.BackgroundError != nil {
		t.Fatalf("storage stats = %#v", stats)
	}
}
