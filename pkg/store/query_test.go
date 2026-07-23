package store

import "testing"

func TestStoreScanMergesVersionsAndTombstones(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for key, value := range map[string]string{"a": "old-a", "b": "old-b", "c": "old-c"} {
		if err := store.Put(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("b", "new-b"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("c"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("d", "new-d"); err != nil {
		t.Fatal(err)
	}

	records, err := store.Scan("b", "e")
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{{Key: "b", Val: "new-b"}, {Key: "d", Val: "new-d"}}
	if len(records) != len(want) {
		t.Fatalf("Scan() = %#v, want %#v", records, want)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Fatalf("Scan()[%d] = %#v, want %#v", i, records[i], want[i])
		}
	}
}

func TestSStableScanEndKeyIsExclusive(t *testing.T) {
	table, err := NewSStable([]Record{
		{Key: "a", Val: "1"},
		{Key: "b", Val: "2"},
		{Key: "c", Val: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := table.Scan("a", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Key != "a" || records[1].Key != "b" {
		t.Fatalf("Scan(a,c) = %#v", records)
	}
}
