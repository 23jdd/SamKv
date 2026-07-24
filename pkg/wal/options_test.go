package wal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewWithOptionsRejectsInvalidConfiguration(t *testing.T) {
	tests := []Options{
		{BufferSize: 0, SyncPolicy: SyncInterval, SyncInterval: time.Second},
		{BufferSize: DefaultSize, SyncPolicy: SyncPolicy(99), SyncInterval: time.Second},
		{BufferSize: DefaultSize, SyncPolicy: SyncInterval, SyncInterval: 0},
	}
	for _, options := range tests {
		if _, err := NewWithOptions(t.TempDir(), options); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("NewWithOptions(%+v) error = %v, want ErrInvalidOptions", options, err)
		}
	}
}

func TestSyncEveryWritePersistsBeforeAppendReturns(t *testing.T) {
	options := DefaultOptions()
	options.SyncPolicy = SyncEveryWrite
	options.SyncInterval = 0
	writer, err := NewWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := writer.AppendRecord(PutRecord([]byte("strict"), []byte("durable"))); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(filepath.Join(writer.Dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	record, err := ReadRecord(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(record.Key) != "strict" || string(record.Value) != "durable" {
		t.Fatalf("record = %#v", record)
	}
}

func TestSyncIntervalDefersSmallWriteUntilFlush(t *testing.T) {
	options := DefaultOptions()
	options.SyncInterval = time.Hour
	writer, err := NewWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := writer.AppendRecord(PutRecord([]byte("buffered"), []byte("value"))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(writer.Dir, "wal.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("wal size before Flush = %d, want 0", info.Size())
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("wal is empty after Flush")
	}
}

func TestSyncIntervalFlushesFullBufferWithoutWaitingForTicker(t *testing.T) {
	options := DefaultOptions()
	options.BufferSize = 8
	options.SyncInterval = time.Hour
	writer, err := NewWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := writer.AppendLog([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	appendDone := make(chan error, 1)
	go func() {
		appendDone <- writer.AppendLog([]byte("7890"))
	}()

	select {
	case err := <-appendDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("append waited for the periodic flush after the WAL buffer became full")
	}

	info, err := os.Stat(filepath.Join(writer.Dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 6 {
		t.Fatalf("wal size after full-buffer flush = %d, want 6", info.Size())
	}
}
