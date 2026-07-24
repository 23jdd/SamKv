package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStorePreventsConcurrentOpenOfDataDirectory(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0

	first, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := NewStoreManagerWithOptions(dir, options); !errors.Is(err, ErrDataDirLocked) {
		t.Fatalf("second open error = %v, want ErrDataDirLocked", err)
	}

	owner, err := os.ReadFile(filepath.Join(dir, "LOCK"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(owner), "pid="+strconv.Itoa(os.Getpid())) {
		t.Fatalf("LOCK contents = %q", owner)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatalf("open after Close error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
