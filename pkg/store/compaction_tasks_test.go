package store

import (
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlanCompactionRangesUsesSortedUniqueBoundaries(t *testing.T) {
	tables := []*SStable{
		{index: []IndexEntry{{FirstKey: "a"}, {FirstKey: "e"}, {FirstKey: "i"}}},
		{index: []IndexEntry{{FirstKey: "a"}, {FirstKey: "m"}, {FirstKey: "z"}}},
	}
	got := planCompactionRanges(tables, []int{0, 1}, 3)
	want := []compactionRange{
		{endKey: "e"},
		{startKey: "e", endKey: "m"},
		{startKey: "m"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planCompactionRanges() = %#v, want %#v", got, want)
	}
}

func TestPlanCompactionRangesCapsWorkersAtUsefulRanges(t *testing.T) {
	tables := []*SStable{{index: []IndexEntry{{FirstKey: "a"}, {FirstKey: "z"}}}}
	got := planCompactionRanges(tables, []int{0}, 8)
	want := []compactionRange{{endKey: "z"}, {startKey: "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planCompactionRanges() = %#v, want %#v", got, want)
	}
}

func TestPlanCompactionRangesFallsBackToSingleTask(t *testing.T) {
	tables := []*SStable{{index: []IndexEntry{{FirstKey: "only"}}}}
	for _, workers := range []int{0, 1, 4} {
		got := planCompactionRanges(tables, []int{0}, workers)
		if !reflect.DeepEqual(got, []compactionRange{{}}) {
			t.Fatalf("workers=%d ranges=%#v", workers, got)
		}
	}
}

func TestPlanCompactionRangesIgnoresUnselectedTables(t *testing.T) {
	tables := []*SStable{
		{index: []IndexEntry{{FirstKey: "a"}, {FirstKey: "b"}}},
		{index: []IndexEntry{{FirstKey: "x"}, {FirstKey: "y"}}},
	}
	got := planCompactionRanges(tables, []int{1}, 2)
	want := []compactionRange{{endKey: "y"}, {startKey: "y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planCompactionRanges() = %#v, want %#v", got, want)
	}
}

func TestRunCompactionRangesStartsTasksInParallel(t *testing.T) {
	ranges := []compactionRange{{endKey: "b"}, {startKey: "b", endKey: "c"}, {startKey: "c"}}
	started := make(chan struct{}, len(ranges))
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32

	done := make(chan error, 1)
	go func() {
		_, err := runCompactionRanges(ranges, func(keyRange compactionRange) (compactionTaskResult, error) {
			current := active.Add(1)
			for {
				oldPeak := peak.Load()
				if current <= oldPeak || peak.CompareAndSwap(oldPeak, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return compactionTaskResult{keyRange: keyRange}, nil
		})
		done <- err
	}()

	for range ranges {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("compaction tasks did not start concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != int32(len(ranges)) {
		t.Fatalf("peak parallel tasks = %d, want %d", got, len(ranges))
	}
}

func TestRunCompactionTasksPreservesNewestRecordAcrossRanges(t *testing.T) {
	oldTable, err := NewSStable([]Record{{Key: "a", Val: "old-a"}, {Key: "z", Val: "old-z"}})
	if err != nil {
		t.Fatal(err)
	}
	newTable, err := NewSStable([]Record{{Key: "a", Val: "new-a"}, {Key: "m", Val: "new-m"}})
	if err != nil {
		t.Fatal(err)
	}
	ranges := []compactionRange{{endKey: "m"}, {startKey: "m"}}
	results, err := runCompactionTasks(
		[]*SStable{oldTable, newTable},
		[]int{0, 1},
		ranges,
		false,
		DefaultOptions(),
		time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].inputRecords != 2 || results[1].inputRecords != 2 {
		t.Fatalf("task results = %#v", results)
	}
	want := [][]Record{
		{{Key: "a", Val: "new-a"}},
		{{Key: "m", Val: "new-m"}, {Key: "z", Val: "old-z"}},
	}
	for index := range want {
		if !reflect.DeepEqual(results[index].records, want[index]) {
			t.Fatalf("task %d records = %#v, want %#v", index, results[index].records, want[index])
		}
	}
}

func TestRunCompactionTasksRejectsInvalidInputTable(t *testing.T) {
	_, err := runCompactionTasks(
		[]*SStable{nil},
		[]int{0},
		[]compactionRange{{}},
		false,
		DefaultOptions(),
		time.Now,
	)
	if !errors.Is(err, ErrInvalidSSTable) {
		t.Fatalf("runCompactionTasks() error = %v, want %v", err, ErrInvalidSSTable)
	}
}
