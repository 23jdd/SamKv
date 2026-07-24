package store

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

// CompactNextLevel ??????????????????????????
func (st *StoreManger) CompactNextLevel() (CompactionResult, error) {
	st.mu.RLock()
	level := st.nextCompactionLevelLocked()
	st.mu.RUnlock()
	if level < 0 {
		return CompactionResult{}, nil
	}
	return st.CompactLevel(level)
}

// CompactLevel ??? source level ???? key ????? SSTable?
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

	latest := make(map[string]Record)
	for _, index := range selection.indexes {
		records, err := tables[index].AllRecords()
		if err != nil {
			return result, err
		}
		result.InputRecords += len(records)
		for _, record := range records {
			latest[record.Key] = record
		}
	}
	records := compactLevelRecords(latest, selection.targetLevel == options.MaxLevels-1, options, now)
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
		cleanupCompactionOutput(newTable, path)
		return result, ErrStoreClosed
	}
	if !sameTables(st.sstables, tables) {
		st.mu.Unlock()
		cleanupCompactionOutput(newTable, path)
		return result, errors.New("store: sstable set changed during level compaction")
	}

	nextTables := make([]*SStable, 0, len(tables)-len(selected)+1)
	nextEntries := make([]ManifestSSTable, 0, len(tables)-len(selected)+1)
	for index, table := range tables {
		if _, ok := selected[index]; !ok {
			nextTables = append(nextTables, table)
			nextEntries = append(nextEntries, manifest.SSTables[index])
		}
		if index == insertAt && newTable != nil {
			entry := manifestEntryFromSSTable(path, newTable)
			entry.Level = selection.targetLevel
			nextTables = append(nextTables, newTable)
			nextEntries = append(nextEntries, entry)
		}
	}

	nextManifest := manifest
	nextManifest.SSTables = nextEntries
	nextManifest.LastSequence = st.sequence.Load()
	if newTable != nil {
		nextManifest.NextFileID = st.nextSSTableID + 1
	}
	if err := saveManifest(st.dir, nextManifest); err != nil {
		st.mu.Unlock()
		cleanupCompactionOutput(newTable, path)
		return result, err
	}
	st.sstables = nextTables
	st.manifest = nextManifest
	if newTable != nil {
		st.nextSSTableID++
	}
	st.mu.Unlock()

	var cleanupErr error
	for index := range selected {
		table := tables[index]
		oldPath := table.Path()
		cleanupErr = errors.Join(cleanupErr, table.Close())
		st.blockCache.removeFile(oldPath)
		if oldPath != "" && oldPath != path {
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
	if table != nil {
		_ = table.Close()
	}
	if path != "" {
		_ = os.Remove(path)
	}
}
