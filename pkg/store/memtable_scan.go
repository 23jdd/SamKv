package store

import "sort"

// Scan 返回 [startKey, endKey) 范围内的有序记录快照。
// 空边界表示不限制；返回结果包含墓碑，供 Store 做多版本合并。
func (mt *MemTable) Scan(startKey, endKey string) []Record {
	records := mt.Entries()
	start := 0
	if startKey != "" {
		start = sort.Search(len(records), func(i int) bool {
			return records[i].Key >= startKey
		})
	}
	end := len(records)
	if endKey != "" {
		end = sort.Search(len(records), func(i int) bool {
			return records[i].Key >= endKey
		})
	}
	if start >= end {
		return nil
	}

	out := make([]Record, end-start)
	copy(out, records[start:end])
	return out
}
