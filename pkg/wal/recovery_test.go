package wal

// 本文件验证多 segment 顺序回放、完整坏帧跳过、末段截断修复和中间段损坏拒绝。

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

func encodedPut(t testing.TB, key string) []byte {
	t.Helper()
	data, err := PutRecord([]byte(key), []byte("value")).Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReplaySegmentsSkipsChecksummedRecordAndContinues(t *testing.T) {
	dir := t.TempDir()
	first := encodedPut(t, "first")
	corrupt := encodedPut(t, "corrupt")
	corrupt[0] ^= 0xff
	last := encodedPut(t, "last")
	if err := os.WriteFile(SegmentPath(dir, 1), bytes.Join([][]byte{first, corrupt, last}, nil), 0644); err != nil {
		t.Fatal(err)
	}

	var keys []string
	report, err := ReplaySegments(dir, DefaultRecoveryOptions(), func(record *Record) error {
		keys = append(keys, string(record.Key))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Records != 2 || report.SkippedRecords != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !bytes.Equal([]byte(keys[0]+","+keys[1]), []byte("first,last")) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestReplaySegmentsRepairsPartialTailOnlyInLastSegment(t *testing.T) {
	dir := t.TempDir()
	complete := encodedPut(t, "complete")
	path := SegmentPath(dir, 1)
	if err := os.WriteFile(path, append(append([]byte(nil), complete...), 1, 2, 3), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := ReplaySegments(dir, DefaultRecoveryOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.TruncatedBytes != 3 {
		t.Fatalf("TruncatedBytes = %d, want 3", report.TruncatedBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, complete) {
		t.Fatal("last segment was not truncated to the complete record boundary")
	}
}

func TestReplaySegmentsRejectsPartialNonLastSegment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(SegmentPath(dir, 1), []byte{1, 2, 3}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SegmentPath(dir, 2), encodedPut(t, "later"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReplaySegments(dir, DefaultRecoveryOptions(), nil)
	if !errors.Is(err, ErrCorruptSegment) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReplaySegments() error = %v", err)
	}
}

func TestReplaySegmentsCanFailFastOnChecksumError(t *testing.T) {
	dir := t.TempDir()
	corrupt := encodedPut(t, "bad")
	corrupt[0] ^= 0xff
	if err := os.WriteFile(SegmentPath(dir, 1), corrupt, 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultRecoveryOptions()
	options.SkipCorruptedRecords = false
	_, err := ReplaySegments(dir, options, nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("ReplaySegments() error = %v, want ErrChecksum", err)
	}
}
