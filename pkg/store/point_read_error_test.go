package store

import (
	"errors"
	"testing"
)

func TestGetWithErrorReportsCorruptSSTableAndHealthFailure(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put("key", "value"); err != nil {
		t.Fatal(err)
	}
	path, err := database.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handle := database.sstables[0].Index()[0].Handle
	flipFileByte(t, path, int64(handle.Offset))

	if _, _, err := database.GetWithError("key"); !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("GetWithError() error = %v, want ErrBlockChecksum", err)
	}
	if !errors.Is(database.BackgroundError(), ErrBlockChecksum) {
		t.Fatalf("BackgroundError() = %v", database.BackgroundError())
	}
	if value, ok := database.Get("key"); ok || value != "" {
		t.Fatalf("Get(key) = %q, %v", value, ok)
	}
}
