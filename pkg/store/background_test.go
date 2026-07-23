package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutoCheckpointKeepsWritesVisibleAndRewritesWAL(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.MemTableLimit = ComputeSize(len("large"), 256)
	options.CompactionThreshold = 0

	store, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("large", strings.Repeat("x", 256)); err != nil {
		t.Fatal(err)
	}

	waitForStoreCondition(t, func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return len(store.immutables) == 0 && len(store.sstables) == 1 && store.backgroundErr == nil
	})
	if value, ok := store.Get("large"); !ok || len(value) != 256 {
		t.Fatalf("Get(large) = len:%d, %v", len(value), ok)
	}

	if err := store.Put("pending", "wal-only"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	options.AutoCheckpoint = false
	reopened, err := NewStoreMangerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for key, want := range map[string]string{
		"large":   strings.Repeat("x", 256),
		"pending": "wal-only",
	} {
		if got, ok := reopened.Get(key); !ok || got != want {
			t.Fatalf("Get(%q) = %q, %v", key, got, ok)
		}
	}
	if reopened.mem.Len() != 1 {
		t.Fatalf("recovered active MemTable len = %d, want 1", reopened.mem.Len())
	}
}

func waitForStoreCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background maintenance")
}

func TestConcurrentWritesDuringAutomaticMemTableSwitch(t *testing.T) {
	options := DefaultOptions()
	options.MemTableLimit = 512
	options.CompactionThreshold = 0
	store, err := NewStoreMangerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const goroutines = 8
	const writesPerGoroutine = 50
	var waitGroup sync.WaitGroup
	errCh := make(chan error, goroutines)
	for worker := 0; worker < goroutines; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				key := fmt.Sprintf("%02d-%03d", worker, i)
				if err := store.Put(key, strings.Repeat("v", 32)); err != nil {
					errCh <- err
					return
				}
			}
		}(worker)
	}
	waitGroup.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for worker := 0; worker < goroutines; worker++ {
		for i := 0; i < writesPerGoroutine; i++ {
			key := fmt.Sprintf("%02d-%03d", worker, i)
			if value, ok := store.Get(key); !ok || len(value) != 32 {
				t.Fatalf("Get(%q) = len:%d, %v", key, len(value), ok)
			}
		}
	}
}

func TestAutomaticCompactionAtTableThreshold(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 2
	store, err := NewStoreMangerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Put("a", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("b", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	waitForStoreCondition(t, func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return len(store.sstables) == 1 &&
			len(store.manifest.SSTables) == 1 &&
			store.manifest.SSTables[0].Level == 1 &&
			store.backgroundErr == nil
	})
}
