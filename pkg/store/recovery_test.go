package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAutomaticallyRecoversWAL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	if err := store.Put("pending", "value"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("reopen NewStoreManger() error = %v", err)
	}
	defer reopened.Close()

	value, ok := reopened.Get("pending")
	if !ok || value != "value" {
		t.Fatalf("Get(pending) = %q, %v; want value, true", value, ok)
	}
}

func TestStoreRepairsIncompleteWALTailBeforeAppending(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	if err := store.Put("before", "crash"); err != nil {
		t.Fatalf("Put(before) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	walPath := filepath.Join(dir, "wal.log")
	info, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	validSize := info.Size()

	file, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("reopen with incomplete tail error = %v", err)
	}
	if value, ok := reopened.Get("before"); !ok || value != "crash" {
		t.Fatalf("Get(before) = %q, %v", value, ok)
	}
	info, err = os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != validSize {
		t.Fatalf("repaired WAL size = %d, want %d", info.Size(), validSize)
	}
	if err := reopened.Put("after", "restart"); err != nil {
		t.Fatalf("Put(after) error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("second reopen error = %v", err)
	}
	defer again.Close()
	if value, ok := again.Get("after"); !ok || value != "restart" {
		t.Fatalf("Get(after) = %q, %v", value, ok)
	}
}
