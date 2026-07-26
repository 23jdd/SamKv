package wal

// 本文件验证 WAL 按字节和 record 数轮转，且批量追加只在完整 record 边界切段。

import (
	"bytes"
	"os"
	"testing"
)

func strictSegmentOptions() Options {
	options := DefaultOptions()
	options.SyncPolicy = SyncEveryWrite
	options.SyncInterval = 0
	return options
}

func TestWALRotatesBySegmentSizeWithoutSplittingRecord(t *testing.T) {
	options := strictSegmentOptions()
	options.SegmentSize = 48
	manager, err := NewWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}

	first := PutRecord([]byte("first"), []byte("value"))
	second := PutRecord([]byte("second"), []byte("value"))
	if err := manager.AppendRecord(first); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendRecord(second); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(manager.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 {
		t.Fatalf("segment count = %d, want 2", len(segments))
	}
	for _, segment := range segments {
		file, err := os.Open(segment.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRecord(file); err != nil {
			_ = file.Close()
			t.Fatalf("segment %d does not contain a complete record: %v", segment.ID, err)
		}
		_ = file.Close()
	}
}

func TestWALRotatesBatchByRecordCount(t *testing.T) {
	options := strictSegmentOptions()
	options.SegmentSize = 1 << 20
	options.SegmentMaxRecords = 2
	manager, err := NewWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}

	var batch []byte
	for i := 0; i < 5; i++ {
		encoded, err := PutRecord([]byte{byte('a' + i)}, []byte("v")).Encode()
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, encoded...)
	}
	if err := manager.AppendLog(batch); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(manager.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 {
		t.Fatalf("segment count = %d, want 3", len(segments))
	}
	counts := make([]uint64, len(segments))
	for i, segment := range segments {
		counts[i] = countSegmentRecords(segment.Path)
	}
	if !bytes.Equal([]byte{byte(counts[0]), byte(counts[1]), byte(counts[2])}, []byte{2, 2, 1}) {
		t.Fatalf("records per segment = %v, want [2 2 1]", counts)
	}
}

func TestRotateSealsCurrentSegment(t *testing.T) {
	manager, err := NewWithOptions(t.TempDir(), strictSegmentOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.AppendRecord(PutRecord([]byte("key"), []byte("value"))); err != nil {
		t.Fatal(err)
	}

	sealed, err := manager.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if sealed != 1 || manager.ActiveSegment().ID != 2 {
		t.Fatalf("Rotate() = %d, active = %d", sealed, manager.ActiveSegment().ID)
	}
}
