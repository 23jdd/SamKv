package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDataBlockRoundTripWithPrefixCompression(t *testing.T) {
	records := []Record{
		{Key: "user:0001:name", Val: "alice"},
		{Key: "user:0001:role", Val: "admin"},
		{Key: "user:0002:name", Val: "bob"},
	}

	data, err := EncodeDataBlock(records)
	if err != nil {
		t.Fatalf("EncodeDataBlock() error = %v", err)
	}
	got, err := DecodeDataBlock(data)
	if err != nil {
		t.Fatalf("DecodeDataBlock() error = %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("decoded %d records, want %d", len(got), len(records))
	}
	for i := range records {
		if got[i] != records[i] {
			t.Fatalf("record %d = %#v, want %#v", i, got[i], records[i])
		}
	}
}

func TestWriteOpenSStableGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "000001.sst")
	records := []Record{
		{Key: "k003", Val: "v3"},
		{Key: "k001", Val: "v1"},
		{Key: "k002", Val: "v2"},
	}

	created, err := WriteSStable(path, records)
	if err != nil {
		t.Fatalf("WriteSStable() error = %v", err)
	}
	if created.Meta().RecordCount != 3 {
		t.Fatalf("created meta count = %d, want 3", created.Meta().RecordCount)
	}

	table, err := OpenSStable(path)
	if err != nil {
		t.Fatalf("OpenSStable() error = %v", err)
	}
	defer table.Close()

	value, ok, err := table.Get("k002")
	if err != nil {
		t.Fatalf("Get(k002) error = %v", err)
	}
	if !ok || value != "v2" {
		t.Fatalf("Get(k002) = %q, %v; want v2, true", value, ok)
	}

	_, ok, err = table.Get("missing")
	if err != nil {
		t.Fatalf("Get(missing) error = %v", err)
	}
	if ok {
		t.Fatal("Get(missing) found key, want absent")
	}

	meta := table.Meta()
	if meta.MinKey != "k001" || meta.MaxKey != "k003" {
		t.Fatalf("meta range = %q..%q, want k001..k003", meta.MinKey, meta.MaxKey)
	}
	if !meta.Filter.ContainsString("k001") {
		t.Fatal("bloom filter does not contain written key")
	}
}

func TestWriteSStableSplitsDataBlocksAndUsesIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.sst")
	records := make([]Record, 0, 80)
	for i := 0; i < 80; i++ {
		records = append(records, Record{
			Key: strings.Repeat("prefix:", 4) + string(rune('a'+i%26)) + strings.Repeat("x", i%7),
			Val: strings.Repeat("value", 30),
		})
	}

	table, err := WriteSStable(path, records)
	if err != nil {
		t.Fatalf("WriteSStable() error = %v", err)
	}
	if len(table.Index()) < 2 {
		t.Fatalf("index has %d data block(s), want at least 2", len(table.Index()))
	}
}
func TestSStableTombstoneRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tombstone.sst")
	_, err := WriteSStable(path, []Record{
		{Key: "alive", Val: "value"},
		{Key: "gone", Deleted: true},
	})
	if err != nil {
		t.Fatalf("WriteSStable() error = %v", err)
	}

	table, err := OpenSStable(path)
	if err != nil {
		t.Fatalf("OpenSStable() error = %v", err)
	}
	defer table.Close()

	if value, ok, err := table.Get("alive"); err != nil || !ok || value != "value" {
		t.Fatalf("Get(alive) = %q, %v, %v; want value, true, nil", value, ok, err)
	}
	if value, ok, err := table.Get("gone"); err != nil || ok || value != "" {
		t.Fatalf("Get(gone) = %q, %v, %v; want empty, false, nil", value, ok, err)
	}
}
