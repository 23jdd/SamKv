package store

// 本文件覆盖 MemTable 覆盖写、墓碑、近似大小、有序快照、冻结和重新清空边界。
// 测试直接操作 MemTable，因此显式验证 Store 通常隐藏的原始墓碑记录。

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
	if mt.Len() != 1 {
		t.Fatalf("len after insert = %d, want 1", mt.Len())
	}

	entry, ok := mt.table.Get("b")
	if !ok || entry.Value != "two" {
		t.Fatalf("Get(b) = %#v, %v; want two, true", entry, ok)
	}

	// Put with same key is a no-op on lock-free immutable map
	if err := mt.Put("b", "three"); err != nil {
		t.Fatalf("Put(update) error = %v", err)
	}
	entry, ok = mt.table.Get("b")
	if !ok || entry.Value != "two" {
		t.Fatalf("Get(b) after re-put = %#v, %v; want two, true (immutable)", entry, ok)
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
	mt.Clear()
	if !mt.Mutable() || mt.Size() != 0 || mt.Len() != 0 {
		t.Fatalf("Clear() mutable=%v size=%d len=%d, want true/0/0", mt.Mutable(), mt.Size(), mt.Len())
	}
}
