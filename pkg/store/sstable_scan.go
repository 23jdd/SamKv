package store

import "sort"

// Scan 按 key 范围读取 SSTable 中的原始记录。
// 范围采用 [startKey, endKey) 语义；空边界表示不限制，结果包含墓碑。
func (s *SStable) Scan(startKey, endKey string) ([]Record, error) {
	return s.scan(startKey, endKey, true)
}

func (s *SStable) scan(startKey, endKey string, useCache bool) ([]Record, error) {
	if s == nil {
		return nil, ErrInvalidSSTable
	}
	if endKey != "" && startKey != "" && startKey >= endKey {
		return nil, nil
	}
	if s.meta.RecordCount == 0 || !keyRangesOverlap(startKey, endKey, s.meta.MinKey, s.meta.MaxKey) {
		return nil, nil
	}

	if s.file == nil {
		return scanSortedRecords(s.rs, startKey, endKey), nil
	}

	records := make([]Record, 0)
	for _, entry := range s.index {
		if startKey != "" && entry.LastKey < startKey {
			continue
		}
		if endKey != "" && entry.FirstKey >= endKey {
			break
		}

		blockData, release, err := s.readDataBlock(entry.Handle, useCache)
		if err != nil {
			return nil, err
		}
		blockRecords, err := DecodeDataBlock(blockData)
		release()
		if err != nil {
			return nil, err
		}
		records = append(records, scanSortedRecords(blockRecords, startKey, endKey)...)
	}
	return records, nil
}

// AllRecords 返回整张 SSTable 的有序记录，主要供 Compaction 使用。
func (s *SStable) AllRecords() ([]Record, error) {
	return s.Scan("", "")
}

// Path 返回 SSTable 的磁盘路径。
func (s *SStable) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func scanSortedRecords(records []Record, startKey, endKey string) []Record {
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

func keyRangesOverlap(startKey, endKey, minKey, maxKey string) bool {
	if minKey == "" && maxKey == "" {
		return false
	}
	if startKey != "" && maxKey < startKey {
		return false
	}
	if endKey != "" && minKey >= endKey {
		return false
	}
	return true
}
