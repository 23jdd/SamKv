package store

// 本文件验证 Store 按 segment 顺序恢复、跳过完整坏 record，并保留恢复统计。

import (
	"os"
	"testing"

	"github.com/23jdd/SamKv/pkg/wal"
)

func TestRecoverWALDirectoryReplaysAllSegments(t *testing.T) {
	dir := t.TempDir()
	options := wal.DefaultOptions()
	options.SyncPolicy = wal.SyncEveryWrite
	options.SyncInterval = 0
	options.SegmentMaxRecords = 1
	manager, err := wal.NewWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendRecord(wal.PutRecord([]byte("a"), []byte("1"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendRecord(wal.PutRecord([]byte("b"), []byte("2"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	mem := NewMemTable(0)
	report, err := RecoverWALDirectory(dir, mem)
	if err != nil {
		t.Fatal(err)
	}
	if report.Segments != 2 || report.Records != 2 {
		t.Fatalf("report = %+v", report)
	}
	if entry, ok := mem.table.Get("a"); !ok || entry.Value != "1" {
		t.Fatalf("Get(a) = %#v, %v", entry, ok)
	}
	if entry, ok := mem.table.Get("b"); !ok || entry.Value != "2" {
		t.Fatalf("Get(b) = %#v, %v", entry, ok)
	}
}

func TestRecoverWALDirectorySkipsCompleteCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	good, err := wal.PutRecord([]byte("good"), []byte("value")).Encode()
	if err != nil {
		t.Fatal(err)
	}
	bad, err := wal.PutRecord([]byte("bad"), []byte("value")).Encode()
	if err != nil {
		t.Fatal(err)
	}
	bad[0] ^= 0xff
	if err := os.WriteFile(wal.SegmentPath(dir, 1), append(bad, good...), 0644); err != nil {
		t.Fatal(err)
	}

	mem := NewMemTable(0)
	report, err := RecoverWALDirectory(dir, mem)
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedRecords != 1 || report.Records != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, ok := mem.table.Get("bad"); ok {
		t.Fatal("corrupt record was applied")
	}
	if entry, ok := mem.table.Get("good"); !ok || entry.Value != "value" {
		t.Fatalf("Get(good) = %#v, %v", entry, ok)
	}
}
