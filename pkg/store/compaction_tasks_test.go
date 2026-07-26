package store

import (
	"reflect"
	"testing"
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
