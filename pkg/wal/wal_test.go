package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendRecordLargerThanBufferWritesDirectly(t *testing.T) {
	wm, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer wm.Close()

	value := bytes.Repeat([]byte("x"), DefaultSize)
	record := PutRecord([]byte("large-key"), value)

	done := make(chan error, 1)
	go func() {
		done <- wm.AppendRecord(record)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AppendRecord() timed out for record larger than WAL buffer")
	}

	reader, err := os.Open(filepath.Join(wm.Dir, "wal.log"))
	if err != nil {
		t.Fatalf("Open wal.log error = %v", err)
	}
	defer reader.Close()

	got, err := ReadRecord(reader)
	if err != nil {
		t.Fatalf("ReadRecord() error = %v", err)
	}
	if got.Type != RecordPut || string(got.Key) != "large-key" || !bytes.Equal(got.Value, value) {
		t.Fatalf("ReadRecord() = %#v, want large put record", got)
	}
}

func TestReplaceRewritesWALContents(t *testing.T) {
	dir := t.TempDir()
	wm, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wm.AppendRecord(PutRecord([]byte("old"), []byte("value"))); err != nil {
		t.Fatal(err)
	}
	if err := wm.Flush(); err != nil {
		t.Fatal(err)
	}

	replacement, err := PutRecord([]byte("new"), []byte("value")).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := wm.Replace(replacement); err != nil {
		t.Fatal(err)
	}
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, replacement) {
		t.Fatalf("wal.log = %x, want %x", got, replacement)
	}
}
