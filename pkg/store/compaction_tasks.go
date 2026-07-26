package store

import "sort"

// compactionRange 描述一个左闭右开的 key 区间；空边界表示没有下限或上限。
type compactionRange struct {
	startKey string
	endKey   string
}

// planCompactionRanges 利用 SSTable 的 DataBlock 首 key 选择近似均匀的分割点。
// 分割点只来自真实记录，因此不会截断 key，并且所有返回区间互不重叠且覆盖完整 key 空间。
func planCompactionRanges(tables []*SStable, indexes []int, workers int) []compactionRange {
	if workers <= 1 {
		return []compactionRange{{}}
	}

	boundarySet := make(map[string]struct{})
	for _, tableIndex := range indexes {
		if tableIndex < 0 || tableIndex >= len(tables) || tables[tableIndex] == nil {
			continue
		}
		for _, entry := range tables[tableIndex].index {
			if entry.FirstKey != "" {
				boundarySet[entry.FirstKey] = struct{}{}
			}
		}
	}
	boundaries := make([]string, 0, len(boundarySet))
	for key := range boundarySet {
		boundaries = append(boundaries, key)
	}
	sort.Strings(boundaries)
	if len(boundaries) < 2 {
		return []compactionRange{{}}
	}

	taskCount := min(workers, len(boundaries))
	ranges := make([]compactionRange, 0, taskCount)
	startKey := ""
	for task := 1; task < taskCount; task++ {
		boundaryIndex := task * len(boundaries) / taskCount
		endKey := boundaries[boundaryIndex]
		ranges = append(ranges, compactionRange{startKey: startKey, endKey: endKey})
		startKey = endKey
	}
	return append(ranges, compactionRange{startKey: startKey})
}
