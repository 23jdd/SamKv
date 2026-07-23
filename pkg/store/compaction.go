package store

import (
	"errors"
	"os"
	"sort"

	"github.com/23jdd/SamKv/pkg/utils"
)

// CompactionResult 描述一次全量 Compaction 的输入、输出和清理数量。
type CompactionResult struct {
	Path           string
	InputTables    int
	InputRecords   int
	OutputRecords  int
	DroppedRecords int
}

// Compact 合并当前所有 SSTable，只保留每个 key 的最新版本。
// 因为输入覆盖了全部磁盘层，墓碑可安全删除；结构化日志还会应用时间和容量保留策略。
func (st *StoreManger) Compact() (CompactionResult, error) {
	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()

	st.mu.RLock()
	if st.closed {
		st.mu.RUnlock()
		return CompactionResult{}, ErrStoreClosed
	}
	tables := append([]*SStable(nil), st.sstables...)
	options := st.options
	now := st.now
	st.mu.RUnlock()

	result := CompactionResult{InputTables: len(tables)}
	if len(tables) == 0 {
		return result, nil
	}
	if len(tables) == 1 && options.Retention == 0 && options.MaxSizeBytes == 0 {
		return result, nil
	}

	latest := make(map[string]Record)
	for _, table := range tables {
		records, err := table.AllRecords()
		if err != nil {
			return result, err
		}
		result.InputRecords += len(records)
		for _, record := range records {
			latest[record.Key] = record
		}
	}

	cutoff := int64(0)
	hasCutoff := options.Retention > 0
	if hasCutoff {
		cutoff = now().Add(-options.Retention).UnixNano()
	}

	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record := latest[key]
		if record.Deleted {
			continue
		}
		if hasCutoff {
			decoded, err := utils.DecodeKey([]byte(record.Key))
			if err == nil && decoded.Timestamp < cutoff {
				continue
			}
		}
		records = append(records, record)
	}
	records = enforceSizeRetention(records, options.MaxSizeBytes)
	result.OutputRecords = len(records)
	result.DroppedRecords = result.InputRecords - result.OutputRecords

	var (
		newTable *SStable
		path     string
	)
	if len(records) > 0 {
		st.mu.RLock()
		path = st.nextSSTablePathLocked()
		st.mu.RUnlock()

		var err error
		newTable, err = WriteSStable(path, records)
		if err != nil {
			return result, err
		}
		result.Path = path
	}

	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		if newTable != nil {
			_ = newTable.Close()
			_ = os.Remove(path)
		}
		return result, ErrStoreClosed
	}
	if !sameTables(st.sstables, tables) {
		st.mu.Unlock()
		if newTable != nil {
			_ = newTable.Close()
			_ = os.Remove(path)
		}
		return result, errors.New("store: sstable set changed during compaction")
	}

	nextManifest := st.manifest
	nextManifest.SSTables = nil
	if newTable != nil {
		entry := manifestEntryFromSSTable(path, newTable)
		entry.Level = 1
		nextManifest.SSTables = []ManifestSSTable{entry}
		nextManifest.NextFileID = st.nextSSTableID + 1
	}
	nextManifest.LastSequence = st.sequence.Load()
	if err := saveManifest(st.dir, nextManifest); err != nil {
		st.mu.Unlock()
		if newTable != nil {
			_ = newTable.Close()
			_ = os.Remove(path)
		}
		return result, err
	}

	oldTables := st.sstables
	if newTable == nil {
		st.sstables = nil
	} else {
		st.sstables = []*SStable{newTable}
		st.nextSSTableID++
	}
	st.manifest = nextManifest
	st.mu.Unlock()

	var cleanupErr error
	for _, table := range oldTables {
		oldPath := table.Path()
		cleanupErr = errors.Join(cleanupErr, table.Close())
		if oldPath != "" && oldPath != path {
			if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	return result, cleanupErr
}

func enforceSizeRetention(records []Record, maxSizeBytes int64) []Record {
	if maxSizeBytes <= 0 || len(records) == 0 {
		return records
	}

	var total int64
	for _, record := range records {
		total += approximateSSTableRecordSize(record)
	}
	if total <= maxSizeBytes {
		return records
	}

	type candidate struct {
		index     int
		timestamp int64
	}
	candidates := make([]candidate, 0, len(records))
	for i, record := range records {
		key, err := utils.DecodeKey([]byte(record.Key))
		if err == nil {
			candidates = append(candidates, candidate{index: i, timestamp: key.Timestamp})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].timestamp == candidates[j].timestamp {
			return candidates[i].index < candidates[j].index
		}
		return candidates[i].timestamp < candidates[j].timestamp
	})

	dropped := make([]bool, len(records))
	for _, candidate := range candidates {
		if total <= maxSizeBytes {
			break
		}
		dropped[candidate.index] = true
		total -= approximateSSTableRecordSize(records[candidate.index])
	}

	out := make([]Record, 0, len(records))
	for i, record := range records {
		if !dropped[i] {
			out = append(out, record)
		}
	}
	return out
}

func approximateSSTableRecordSize(record Record) int64 {
	return int64(13 + len(record.Key) + len(record.Val))
}

func sameTables(current, snapshot []*SStable) bool {
	if len(current) != len(snapshot) {
		return false
	}
	for i := range current {
		if current[i] != snapshot[i] {
			return false
		}
	}
	return true
}
