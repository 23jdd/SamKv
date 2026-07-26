package store

// 本文件实现跨全部层级的全量 Compaction，并负责按保留时间和容量淘汰结构化日志。
// 常规写入路径应优先使用 CompactLevel；Compact 会读取所有 SSTable，适合手动维护、升级或彻底回收墓碑。

import (
	"errors"
	"os"
	"sort"

	"github.com/23jdd/SamKv/pkg/utils"
)

// CompactionResult 描述一次全量或分层 Compaction 的输入、输出和清理数量。
type CompactionResult struct {
	Path           string   // Path 是第一个输出文件，供只支持单输出的旧调用方兼容使用。
	Paths          []string // Paths 包含本次生成的全部 SSTable；结果全部被淘汰时为空。
	SourceLevel    int      // SourceLevel 为源层级；全量 Compact 使用 -1。
	TargetLevel    int      // TargetLevel 为输出层级。
	InputTables    int      // InputTables 是参与合并的 SSTable 数量。
	OutputTables   int      // OutputTables 是成功发布到 Manifest 的新文件数量。
	Subtasks       int      // Subtasks 是并行 key 范围数量；全量 Compact 固定为 1。
	InputRecords   int      // InputRecords 是所有输入记录数，包含旧版本和墓碑。
	OutputRecords  int      // OutputRecords 是去重、淘汰后的记录数。
	DroppedRecords int      // DroppedRecords 等于 InputRecords-OutputRecords。
}

// Compact 合并当前所有 SSTable，只保留每个 key 的最新版本。
// 因为输入覆盖了全部磁盘层，墓碑可安全删除；结构化日志还会应用时间和容量保留策略。
// 边界条件：空库直接返回零输出；仅有一个表且未配置保留策略时不会重写文件。
func (st *StoreManger) Compact() (CompactionResult, error) {
	return st.compactAll(false)
}

func (st *StoreManger) compactAll(forceRewrite bool) (CompactionResult, error) {
	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()

	st.mu.RLock()
	if st.closed {
		st.mu.RUnlock()
		return CompactionResult{}, ErrStoreClosed
	}
	st.stats.compactions.Add(1)
	tables := append([]*SStable(nil), st.sstables...)
	options := st.options
	now := st.now
	st.mu.RUnlock()

	result := CompactionResult{SourceLevel: -1, TargetLevel: 1, InputTables: len(tables)}
	if len(tables) == 0 {
		return result, nil
	}
	if !forceRewrite && len(tables) == 1 && options.Retention == 0 && options.MaxSizeBytes == 0 {
		return result, nil
	}
	result.Subtasks = 1
	st.stats.compactionTasks.Add(1)

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
		newTable.SetBlockCache(st.blockCache)
		result.Path = path
		result.Paths = []string{path}
		result.OutputTables = 1
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
	st.stats.compactionFiles.Add(uint64(result.OutputTables))
	st.mu.Unlock()

	var cleanupErr error
	for _, table := range oldTables {
		oldPath := table.Path()
		cleanupErr = errors.Join(cleanupErr, table.Close())
		if oldPath != "" && oldPath != path {
			st.blockCache.removeFile(oldPath)
			if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	return result, cleanupErr
}

// enforceSizeRetention 只能根据 utils.EncodeKey 生成的结构化 key 判断新旧顺序。
// 无法解码的普通 KV key 不会因 MaxSizeBytes 被淘汰，因此混合使用两种 key 时总大小可能暂时超过上限。
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
