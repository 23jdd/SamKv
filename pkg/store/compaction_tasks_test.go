package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestWriteCompactionOutputsSkipsEmptyRanges(t *testing.T) {
	dir := t.TempDir()
	results := []compactionTaskResult{
		{keyRange: compactionRange{endKey: "m"}, records: []Record{{Key: "a", Val: "1"}}},
		{keyRange: compactionRange{startKey: "m", endKey: "z"}},
		{keyRange: compactionRange{startKey: "z"}, records: []Record{{Key: "z", Val: "2"}}},
	}
	outputs, err := writeCompactionOutputs(dir, 7, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupCompactionOutputs(outputs)

	if len(outputs) != 2 {
		t.Fatalf("output count = %d, want 2", len(outputs))
	}
	for index, wantID := range []uint64{7, 8} {
		if outputs[index].path != sstablePath(dir, wantID) {
			t.Fatalf("output %d path = %q", index, outputs[index].path)
		}
		if _, err := os.Stat(outputs[index].path); err != nil {
			t.Fatalf("stat output %d: %v", index, err)
		}
	}
}

func TestWriteCompactionOutputsCleansSuccessfulFilesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	results := []compactionTaskResult{
		{keyRange: compactionRange{endKey: "m"}, records: []Record{{Key: "a", Val: "1"}}},
		{keyRange: compactionRange{startKey: "m"}, records: []Record{{Key: "z", Val: "2"}}},
	}
	wantErr := errors.New("write failed")
	_, err := writeCompactionOutputsWithWriter(dir, 7, results, nil, func(path string, records []Record) (*SStable, error) {
		if strings.HasSuffix(path, "00000000000000000008.sst") {
			return nil, wantErr
		}
		return WriteSStable(path, records)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeCompactionOutputsWithWriter() error = %v, want %v", err, wantErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".sst" {
			t.Fatalf("failed output left behind %q", entry.Name())
		}
	}
}

func TestCompactionWorkerCountUsesInputSize(t *testing.T) {
	tables := []*SStable{{index: []IndexEntry{
		{Handle: BlockHandle{Size: 8}},
		{Handle: BlockHandle{Size: 9}},
	}}}
	if got := compactionWorkerCount(tables, []int{0}, 4, 8); got != 3 {
		t.Fatalf("compactionWorkerCount() = %d, want 3", got)
	}
	if got := compactionWorkerCount(tables, []int{0}, 2, 8); got != 2 {
		t.Fatalf("capped compactionWorkerCount() = %d, want 2", got)
	}
	if got := compactionWorkerCount(tables, []int{0}, 4, 64); got != 1 {
		t.Fatalf("small compactionWorkerCount() = %d, want 1", got)
	}
}
