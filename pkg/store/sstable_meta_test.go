package store

import (
	"path/filepath"
	"testing"

	"github.com/23jdd/SamKv/pkg/utils"
)

func TestSStablePersistsTimeAndLabelMetadata(t *testing.T) {
	keyA, err := utils.EncodeKey(10, []utils.Label{
		{Name: "app", Value: "nginx"},
		{Name: "level", Value: "ERROR"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := utils.EncodeKey(20, []utils.Label{
		{Name: "app", Value: "api"},
		{Name: "level", Value: "INFO"},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "meta.sst")
	if _, err := WriteSStable(path, []Record{
		{Key: string(keyA), Val: "a"},
		{Key: string(keyB), Val: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()

	meta := table.Meta()
	if !meta.HasTimeRange || meta.MinTimestamp != 10 || meta.MaxTimestamp != 20 {
		t.Fatalf("time metadata = has:%v %d..%d", meta.HasTimeRange, meta.MinTimestamp, meta.MaxTimestamp)
	}
	if meta.LabelCardinality["app"] != 2 || meta.LabelCardinality["level"] != 2 {
		t.Fatalf("label cardinality = %#v", meta.LabelCardinality)
	}
	if !table.MayContainLabels([]utils.Label{{Name: "app", Value: "nginx"}}) {
		t.Fatal("label filter rejected a written label")
	}
	if table.MayContainLabels([]utils.Label{{Name: "app", Value: "missing"}}) {
		t.Fatal("label filter reported an unwritten label")
	}
	if !table.OverlapsTimeRange(15, 25) || table.OverlapsTimeRange(21, 30) {
		t.Fatal("time range overlap filter returned an unexpected result")
	}
}
