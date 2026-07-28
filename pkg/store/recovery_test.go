package store

// 本文件验证 WAL 自动回放、半条尾记录修复、校验错误传播和删除墓碑恢复。

import (
	"os"
	"testing"

	"github.com/23jdd/SamKv/pkg/wal"
)

func TestStoreAutomaticallyRecoversWAL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("NewStoreManger() error = %v", err)
	}
	putStore(t, store, "pending", "value")
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("reopen NewStoreManger() error = %v", err)
	}
	defer reopened.Close()

	value, ok := getStore(t, reopened, "pending")
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
	putStore(t, store, "before", "crash")
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	segments, err := wal.ListSegments(dir)
	if err != nil || len(segments) != 1 {
		t.Fatalf("ListSegments() = %v, %v", segments, err)
	}
	walPath := segments[0].Path
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
	if value, ok := getStore(t, reopened, "before"); !ok || value != "crash" {
		t.Fatalf("Get(before) = %q, %v", value, ok)
	}
	info, err = os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != validSize {
		t.Fatalf("repaired WAL size = %d, want %d", info.Size(), validSize)
	}
	putStore(t, reopened, "after", "restart")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatalf("second reopen error = %v", err)
	}
	defer again.Close()
	if value, ok := getStore(t, again, "after"); !ok || value != "restart" {
		t.Fatalf("Get(after) = %q, %v", value, ok)
	}
}
