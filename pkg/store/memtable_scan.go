package store

// 本文件实现 MemTable 有序快照上的二分范围裁剪。
// Scan 总是先复制完整 Entries，因此返回值不会随着后续写入变化。

import "sort"

// Scan 返回 [startKey, endKey) 范围内的有序记录快照。
// 空边界表示不限制；返回结果包含墓碑，供 Store 做多版本合并。
// startKey>=endKey 时返回 nil；端点按字节字符串比较，结束键不包含在结果中。
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
