package store

import (
	"errors"
	"sort"
)

var ErrInvalidRange = errors.New("store: invalid key range")

// Scan 合并 MemTable 和所有 SSTable，返回 [startKey, endKey) 内的最新可见记录。
// 相同 key 由较新的 SSTable 或 MemTable 覆盖，最终结果不会包含墓碑。
func (st *StoreManger) Scan(startKey, endKey string) ([]Record, error) {
	return st.scanWithTableFilter(startKey, endKey, nil)
}

func (st *StoreManger) scanWithTableFilter(
	startKey string,
	endKey string,
	includeTable func(*SStable) bool,
) ([]Record, error) {
	if startKey != "" && endKey != "" && startKey >= endKey {
		return nil, ErrInvalidRange
	}

	st.mu.RLock()
	defer st.mu.RUnlock()

	latest := make(map[string]Record)
	// SSTable 按旧到新保存，后遍历到的记录自然覆盖旧版本。
	for _, table := range st.sstables {
		if includeTable != nil && !includeTable(table) {
			continue
		}
		records, err := table.Scan(startKey, endKey)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			latest[record.Key] = record
		}
	}
	for _, record := range st.mem.Scan(startKey, endKey) {
		latest[record.Key] = record
	}

	keys := make([]string, 0, len(latest))
	for key, record := range latest {
		if !record.Deleted {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		records = append(records, latest[key])
	}
	return records, nil
}
