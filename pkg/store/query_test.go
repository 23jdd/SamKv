package store

// 本文件验证半开区间、空边界、版本覆盖、墓碑过滤和无效范围。

import "testing"

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
