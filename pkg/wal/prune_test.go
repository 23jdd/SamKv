package wal

// 本文件验证 segment 回收永远越不过活动段，以及 Replace 先发布新世代再清理旧世代。

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestPruneThroughRemovesOnlySealedSegments(t *testing.T) {
	manager, err := NewWithOptions(t.TempDir(), strictSegmentOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if err := manager.AppendRecord(PutRecord([]byte("old"), []byte("value"))); err != nil {
		t.Fatal(err)
	}
	sealed, err := manager.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendRecord(PutRecord([]byte("active"), []byte("value"))); err != nil {
		t.Fatal(err)
	}

	removed, err := manager.PruneThrough(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	segments, err := ListSegments(manager.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].ID != manager.ActiveSegment().ID {
		t.Fatalf("segments = %+v, active = %+v", segments, manager.ActiveSegment())
	}
}

func TestPruneThroughRejectsActiveSegment(t *testing.T) {
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.PruneThrough(manager.ActiveSegment().ID); !errors.Is(err, ErrInvalidPrune) {
		t.Fatalf("PruneThrough() error = %v, want ErrInvalidPrune", err)
	}
}

func TestReplacePublishesNewSegmentAndRemovesOldSegments(t *testing.T) {
	manager, err := NewWithOptions(t.TempDir(), strictSegmentOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendRecord(PutRecord([]byte("old"), []byte("value"))); err != nil {
		t.Fatal(err)
	}
	replacement := encodedPut(t, "new")
	if err := manager.Replace(replacement); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(manager.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].ID != 2 {
		t.Fatalf("segments = %+v", segments)
	}
	data, err := os.ReadFile(segments[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, replacement) {
		t.Fatalf("replacement data mismatch")
	}
}
