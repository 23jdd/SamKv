package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSSTableVerifyChecksAllRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.sst")
	created, err := WriteSStable(path, []Record{
		{Key: "a", Val: "1"},
		{Key: "b", Val: "2"},
		{Key: "c", Deleted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()

	report, err := table.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != currentSSTableVersion || report.Records != 3 || report.DataBlocks != len(created.Index()) {
		t.Fatalf("verification = %#v", report)
	}
}

func TestSSTableVerifyReportsCorruptDataBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.sst")
	created, err := WriteSStable(path, []Record{{Key: "a", Val: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	flipFileByte(t, path, int64(created.Index()[0].Handle.Offset))

	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	if _, err := table.Verify(); !errors.Is(err, ErrSSTableCorrupt) || !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestStoreVerifyAggregatesPublishedTables(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, key := range []string{"a", "b"} {
		if err := database.Put(key, key); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	report, err := database.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if report.Tables != 2 || report.Records != 2 || len(report.Results) != 2 {
		t.Fatalf("verification = %#v", report)
	}
}
