package store

import (
	"errors"
	"testing"
)

func TestMemTablePutUpdateDeleteAndSize(t *testing.T) {
	mt := NewMemTable(0)

	if err := mt.Put("b", "two"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	wantSize := ComputeSize(len("b"), len("two"))
	if mt.Size() != wantSize {
		t.Fatalf("size after insert = %d, want %d", mt.Size(), wantSize)
	}

	if err := mt.Put("b", "three"); err != nil {
		t.Fatalf("Put(update) error = %v", err)
	}
	wantSize += len("three") - len("two")
	if mt.Size() != wantSize {
		t.Fatalf("size after update = %d, want %d", mt.Size(), wantSize)
	}

	value, ok := mt.Get("b")
	if !ok || value != "three" {
		t.Fatalf("Get(b) = %q, %v; want three, true", value, ok)
	}

	if err := mt.Delete("b"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	wantSize = ComputeSize(len("b"), 0)
	if mt.Size() != wantSize {
		t.Fatalf("size after tombstone = %d, want %d", mt.Size(), wantSize)
	}
	if mt.Len() != 1 {
		t.Fatalf("len after tombstone = %d, want 1", mt.Len())
	}
	if _, ok := mt.Get("b"); ok {
		t.Fatal("Get(b) found value after tombstone")
	}
	entries := mt.Entries()
	if len(entries) != 1 || !entries[0].Deleted || entries[0].Key != "b" {
		t.Fatalf("Entries() after delete = %#v, want one tombstone for b", entries)
	}
}

func TestMemTableEntriesAreSortedRecords(t *testing.T) {
	mt := NewMemTable(0)
	for _, record := range []Record{{Key: "c", Val: "3"}, {Key: "a", Val: "1"}, {Key: "b", Val: "2"}} {
		if err := mt.Put(record.Key, record.Val); err != nil {
			t.Fatalf("Put(%q) error = %v", record.Key, err)
		}
	}

	got := mt.Entries()
	want := []Record{{Key: "a", Val: "1"}, {Key: "b", Val: "2"}, {Key: "c", Val: "3"}}
	if len(got) != len(want) {
		t.Fatalf("Entries() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Entries()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestMemTableImmutableAndShouldFlush(t *testing.T) {
	mt := NewMemTable(ComputeSize(1, 1))
	if mt.ShouldFlush() {
		t.Fatal("empty MemTable should not flush")
	}
	if err := mt.Put("a", "1"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !mt.ShouldFlush() {
		t.Fatal("MemTable should flush after reaching limit")
	}

	mt.MarkImmutable()
	if mt.Mutable() {
		t.Fatal("MemTable is mutable after MarkImmutable")
	}
	if err := mt.Put("b", "2"); !errors.Is(err, ErrImmutableMemTable) {
		t.Fatalf("Put() error = %v, want ErrImmutableMemTable", err)
	}
	if err := mt.Delete("a"); !errors.Is(err, ErrImmutableMemTable) {
		t.Fatalf("Delete() error = %v, want ErrImmutableMemTable", err)
	}

	mt.Clear()
	if !mt.Mutable() || mt.Size() != 0 || mt.Len() != 0 {
		t.Fatalf("Clear() mutable=%v size=%d len=%d, want true/0/0", mt.Mutable(), mt.Size(), mt.Len())
	}
}
