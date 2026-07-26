package store

// 本文件验证磁盘/内存迭代器的半开区间、跨 DataBlock 和墓碑语义。

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestSSTableIteratorCrossesDataBlocks(t *testing.T) {
	records := make([]Record, 80)
	for index := range records {
		records[index] = Record{Key: fmt.Sprintf("key-%03d", index), Val: string(make([]byte, 160))}
	}
	records[17].Deleted = true
	path := filepath.Join(t.TempDir(), "iterator.sst")
	if _, err := WriteSStable(path, records); err != nil {
		t.Fatal(err)
	}
	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	if len(table.Index()) < 2 {
		t.Fatalf("data blocks = %d, want at least 2", len(table.Index()))
	}

	iterator, err := table.NewIterator("key-010", "key-025")
	if err != nil {
		t.Fatal(err)
	}
	defer iterator.Close()
	var got []Record
	for iterator.Valid() {
		got = append(got, iterator.Record())
		iterator.Next()
	}
	if err := iterator.Error(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 15 || got[0].Key != "key-010" || got[len(got)-1].Key != "key-024" {
		t.Fatalf("iterator range = %d records, %q..%q", len(got), got[0].Key, got[len(got)-1].Key)
	}
	if !got[7].Deleted {
		t.Fatal("iterator dropped tombstone at key-017")
	}
}

func TestInMemorySSTableIteratorBoundaries(t *testing.T) {
	table, err := NewSStable([]Record{{Key: "c"}, {Key: "a"}, {Key: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	iterator, err := table.NewIterator("b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if !iterator.Valid() || iterator.Record().Key != "b" {
		t.Fatalf("first record = %#v", iterator.Record())
	}
	iterator.Next()
	if iterator.Valid() || iterator.Error() != nil {
		t.Fatalf("iterator after end: valid=%v error=%v", iterator.Valid(), iterator.Error())
	}
	if err := iterator.Close(); err != nil {
		t.Fatal(err)
	}
}
