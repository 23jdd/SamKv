package skiplist

import (
	"sync"
	"testing"
)

func intCompare(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestSkipListOrderedOperations(t *testing.T) {
	list := New[int, string](intCompare)
	if !list.IsEmpty() {
		t.Fatal("new SkipList should be empty")
	}
	if !list.Add(3, "three") || !list.Add(1, "one") || !list.Add(2, "two") {
		t.Fatal("Add() rejected a new key")
	}
	if list.Add(2, "duplicate") {
		t.Fatal("Add() accepted a duplicate key")
	}
	if old, replaced := list.Append(2, "TWO"); !replaced || old != "two" {
		t.Fatalf("Append() = %q, %v", old, replaced)
	}
	if value, ok := list.Get(2); !ok || value != "two" {
		t.Fatalf("Get(2) = %q, %v", value, ok)
	}
	if key, value, ok := list.LowerBound(2); !ok || key != 2 || value != "two" {
		t.Fatalf("LowerBound(2) = %d, %q, %v", key, value, ok)
	}
	if key, _, ok := list.First(); !ok || key != 1 {
		t.Fatalf("First() key = %d, %v", key, ok)
	}
	if key, _, ok := list.Last(); !ok || key != 3 {
		t.Fatalf("Last() key = %d, %v", key, ok)
	}

	entries := list.Entries()
	if len(entries) != 3 || entries[0].Key != 1 || entries[1].Key != 2 || entries[2].Key != 3 {
		t.Fatalf("Entries() = %#v", entries)
	}
	list.Clear()
	if !list.IsEmpty() {
		t.Fatal("Clear() did not empty the SkipList")
	}
}

func TestSkipListRangeCallbackMayMutateList(t *testing.T) {
	list := New[int, int](intCompare)
	for i := 0; i < 5; i++ {
		list.Add(i, i)
	}
	list.Range(func(key, value int) bool {
		list.Append(key, value+10)
		return key < 2
	})
	if value, _ := list.Get(0); value != 0 {
		t.Fatalf("Range callback append immutable node = %d, want 0", value)
	}
}

func TestSkipListConcurrentAccess(t *testing.T) {
	list := New[int, int](intCompare)
	const workers = 8
	const perWorker = 100

	var waitGroup sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < perWorker; i++ {
				key := worker*perWorker + i
				list.Append(key, key)
				list.Get(key)
			}
		}(worker)
	}
	waitGroup.Wait()
	if list.Len() != workers*perWorker {
		t.Fatalf("Len() = %d, want %d", list.Len(), workers*perWorker)
	}
}
