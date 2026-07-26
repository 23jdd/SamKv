package wal

// 本文件验证 WAL segment 文件名是稳定、可排序且不会误接纳临时或近似命名文件。

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSegmentPathAndParseRoundTrip(t *testing.T) {
	path := SegmentPath(t.TempDir(), 42)
	if got, ok := ParseSegmentID(path); !ok || got != 42 {
		t.Fatalf("ParseSegmentID(%q) = %d, %v", path, got, ok)
	}
	if filepath.Base(path) != "wal-00000000000000000042.log" {
		t.Fatalf("SegmentPath() = %q", path)
	}
}

func TestParseSegmentIDRejectsNonCanonicalNames(t *testing.T) {
	for _, name := range []string{
		"wal.log",
		"wal-1.log",
		"wal-00000000000000000000.log",
		"wal-00000000000000000001.log.tmp",
		"other-00000000000000000001.log",
	} {
		if id, ok := ParseSegmentID(name); ok {
			t.Fatalf("ParseSegmentID(%q) = %d, true", name, id)
		}
	}
}

func TestListSegmentsSortsAndIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []uint64{9, 2, 7} {
		if err := os.WriteFile(SegmentPath(dir, id), []byte{byte(id)}, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wal.log"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal-00000000000000000003.log.tmp"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint64
	for _, segment := range segments {
		ids = append(ids, segment.ID)
	}
	if !reflect.DeepEqual(ids, []uint64{2, 7, 9}) {
		t.Fatalf("segment IDs = %v", ids)
	}
}

func activeSegmentPath(t testing.TB, dir string) string {
	t.Helper()
	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("segment count = %d, want 1", len(segments))
	}
	return segments[0].Path
}

func TestNewMigratesLegacyWAL(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyWALFile)
	if err := os.WriteFile(legacy, []byte("legacy-data"), 0644); err != nil {
		t.Fatal(err)
	}

	manager, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy wal still exists: %v", err)
	}
	data, err := os.ReadFile(activeSegmentPath(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legacy-data" {
		t.Fatalf("migrated data = %q", data)
	}
}

func TestNewRejectsMixedLegacyAndSegmentLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, legacyWALFile), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SegmentPath(dir, 1), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); !errors.Is(err, ErrAmbiguousLayout) {
		t.Fatalf("New() error = %v, want ErrAmbiguousLayout", err)
	}
}
