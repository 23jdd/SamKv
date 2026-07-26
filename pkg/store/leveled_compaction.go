package store

// 本文件实现 L0 到末层的增量 Compaction 选择、发布和旧文件回收。
// 顶层 maintenanceMu 会串行化 Checkpoint/Compaction，而选定 key 范围内部可以并行执行。

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

type levelCompactionSelection struct {
	sourceLevel int
	targetLevel int
	indexes     []int
}

// CompactNextLevel 执行当前最需要整理的一层增量 Compaction。
// L0 文件数达到 CompactionThreshold 时优先整理 L0，否则选择首个超过容量上限的非零层；无任务时返回零值结果。
func (st *StoreManger) CompactNextLevel() (CompactionResult, error) {
	st.mu.RLock()
	level := st.nextCompactionLevelLocked()
	st.mu.RUnlock()
	if level < 0 {
		return CompactionResult{}, nil
	}
	return st.CompactLevel(level)
}

// CompactLevel 合并 source level 的候选文件及下一层中 key 范围重叠的 SSTable。
// L0 会选择全部文件，非零层每次只选择一个源文件；level 必须位于 [0, MaxLevels-1)。
// 所有并行输出成功后才原子保存 Manifest，因此失败不会暴露半组结果。
func (st *StoreManger) CompactLevel(level int) (CompactionResult, error) {
	st.maintenanceMu.Lock()
	defer st.maintenanceMu.Unlock()

	st.mu.RLock()
	if st.closed {
		st.mu.RUnlock()
		return CompactionResult{}, ErrStoreClosed
	}
	if level < 0 || level >= st.options.MaxLevels-1 {
		st.mu.RUnlock()
		return CompactionResult{}, ErrInvalidOptions
	}
	tables := append([]*SStable(nil), st.sstables...)
	manifest := st.manifest
	options := st.options
	now := st.now
	selection := selectLevelCompaction(manifest, level)
	st.mu.RUnlock()

	result := CompactionResult{SourceLevel: level, TargetLevel: level + 1, InputTables: len(selection.indexes)}
	if len(selection.indexes) == 0 {
		return result, nil
	}
	st.stats.compactions.Add(1)

	workers := compactionWorkerCount(tables, selection.indexes, options.CompactionWorkers, options.CompactionTaskBytes)
	ranges := planCompactionRanges(tables, selection.indexes, workers)
	result.Subtasks = len(ranges)
	st.stats.compactionTasks.Add(uint64(result.Subtasks))
	taskResults, err := runCompactionTasks(
		tables,
		selection.indexes,
		ranges,
		selection.targetLevel == options.MaxLevels-1,
		options,
		now,
	)
	if err != nil {
		return result, err
	}
	for _, taskResult := range taskResults {
		result.InputRecords += taskResult.inputRecords
		result.OutputRecords += len(taskResult.records)
	}
	result.DroppedRecords = result.InputRecords - result.OutputRecords

	st.mu.RLock()
	firstFileID := st.nextSSTableID
	st.mu.RUnlock()
	outputs, err := writeCompactionOutputs(st.dir, firstFileID, taskResults, st.blockCache)
	if err != nil {
		return result, err
	}
	result.OutputTables = len(outputs)
	result.Paths = make([]string, len(outputs))
	for index, output := range outputs {
		result.Paths[index] = output.path
	}
	if len(result.Paths) > 0 {
		// Path 保留为第一个输出，兼容只处理单输出的旧调用方。
		result.Path = result.Paths[0]
	}

	selected := make(map[int]struct{}, len(selection.indexes))
	insertAt := -1
	for _, index := range selection.indexes {
		selected[index] = struct{}{}
		if index > insertAt {
			insertAt = index
		}
	}

	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		cleanupCompactionOutputs(outputs)
		return result, ErrStoreClosed
	}
	if !sameTables(st.sstables, tables) || st.nextSSTableID != firstFileID {
		st.mu.Unlock()
		cleanupCompactionOutputs(outputs)
		return result, errors.New("store: sstable set changed during level compaction")
	}

	nextTables := make([]*SStable, 0, len(tables)-len(selected)+len(outputs))
	nextEntries := make([]ManifestSSTable, 0, len(tables)-len(selected)+len(outputs))
	for index, table := range tables {
		if _, ok := selected[index]; !ok {
			nextTables = append(nextTables, table)
			nextEntries = append(nextEntries, manifest.SSTables[index])
		}
		if index == insertAt {
			for _, output := range outputs {
				entry := manifestEntryFromSSTable(output.path, output.table)
				entry.Level = selection.targetLevel
				nextTables = append(nextTables, output.table)
				nextEntries = append(nextEntries, entry)
			}
		}
	}

	nextManifest := manifest
	nextManifest.SSTables = nextEntries
	nextManifest.LastSequence = st.sequence.Load()
	if len(outputs) > 0 {
		nextManifest.NextFileID = firstFileID + uint64(len(outputs))
	}
	if err := saveManifest(st.dir, nextManifest); err != nil {
		st.mu.Unlock()
		cleanupCompactionOutputs(outputs)
		return result, err
	}
	st.sstables = nextTables
	st.manifest = nextManifest
	st.stats.compactionFiles.Add(uint64(len(outputs)))
	if len(outputs) > 0 {
		st.nextSSTableID = nextManifest.NextFileID
	}
	st.mu.Unlock()

	var cleanupErr error
	for index := range selected {
		table := tables[index]
		oldPath := table.Path()
		cleanupErr = errors.Join(cleanupErr, table.Close())
		st.blockCache.removeFile(oldPath)
		if oldPath != "" {
			if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	return result, cleanupErr
}

func selectLevelCompaction(manifest Manifest, sourceLevel int) levelCompactionSelection {
	selection := levelCompactionSelection{sourceLevel: sourceLevel, targetLevel: sourceLevel + 1}
	minKey, maxKey := "", ""
	for index, entry := range manifest.SSTables {
		if entry.Level != sourceLevel {
			continue
		}
		selection.indexes = append(selection.indexes, index)
		if sourceLevel > 0 {
			minKey, maxKey = entry.MinKey, entry.MaxKey
			break
		}
		if minKey == "" || entry.MinKey < minKey {
			minKey = entry.MinKey
		}
		if maxKey == "" || entry.MaxKey > maxKey {
			maxKey = entry.MaxKey
		}
	}
	if len(selection.indexes) == 0 {
		return selection
	}
	for index, entry := range manifest.SSTables {
		if entry.Level == selection.targetLevel && rangesOverlapInclusive(minKey, maxKey, entry.MinKey, entry.MaxKey) {
			selection.indexes = append(selection.indexes, index)
		}
	}
	sort.Ints(selection.indexes)
	return selection
}

func rangesOverlapInclusive(firstMin, firstMax, secondMin, secondMax string) bool {
	return firstMin <= secondMax && secondMin <= firstMax
}

func compactLevelRecords(latest map[string]Record, bottomLevel bool, options Options, now func() time.Time) []Record {
	// 非底层必须把墓碑继续向下传递，否则更低层中的旧值会在墓碑被提前删除后重新出现。
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	cutoff := int64(0)
	hasCutoff := bottomLevel && options.Retention > 0
	if hasCutoff {
		cutoff = now().Add(-options.Retention).UnixNano()
	}
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record := latest[key]
		if bottomLevel && record.Deleted {
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
	if bottomLevel {
		records = enforceSizeRetention(records, options.MaxSizeBytes)
	}
	return records
}

func (st *StoreManger) nextCompactionLevelLocked() int {
	if st.options.CompactionThreshold > 0 {
		count := 0
		for _, entry := range st.manifest.SSTables {
			if entry.Level == 0 {
				count++
			}
		}
		if count >= st.options.CompactionThreshold {
			return 0
		}
	}
	for level := 1; level < st.options.MaxLevels-1; level++ {
		if st.levelBytesLocked(level) > st.levelCapacity(level) {
			return level
		}
	}
	return -1
}

func (st *StoreManger) levelBytesLocked(level int) int64 {
	var total int64
	for _, entry := range st.manifest.SSTables {
		if entry.Level != level {
			continue
		}
		if info, err := os.Stat(filepath.Join(st.dir, entry.File)); err == nil {
			total += info.Size()
		}
	}
	return total
}

func (st *StoreManger) levelCapacity(level int) int64 {
	capacity := st.options.LevelBaseSizeBytes
	for current := 1; current < level; current++ {
		if capacity > math.MaxInt64/int64(st.options.LevelSizeMultiplier) {
			return math.MaxInt64
		}
		capacity *= int64(st.options.LevelSizeMultiplier)
	}
	return capacity
}

func cleanupCompactionOutput(table *SStable, path string) {
	if table == nil {
		return
	}
	_ = table.Close()
	if path != "" {
		_ = os.Remove(path)
	}
}
