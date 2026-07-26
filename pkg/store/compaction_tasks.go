package store

// 本文件把一次分层 Compaction 拆成互不重叠的 key 范围，并并行完成读取、归并和 SSTable 写入。

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// compactionRange 描述一个左闭右开的 key 区间；空边界表示没有下限或上限。
type compactionRange struct {
	startKey string
	endKey   string
}

type compactionTaskResult struct {
	keyRange     compactionRange
	records      []Record
	inputRecords int
}

type compactionOutput struct {
	keyRange compactionRange
	records  []Record
	table    *SStable
	path     string
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

func compactionWorkerCount(tables []*SStable, indexes []int, maximum int, taskBytes int64) int {
	if maximum <= 1 || taskBytes <= 0 {
		return 1
	}
	var inputBytes uint64
	for _, tableIndex := range indexes {
		if tableIndex < 0 || tableIndex >= len(tables) || tables[tableIndex] == nil {
			continue
		}
		for _, entry := range tables[tableIndex].index {
			inputBytes += entry.Handle.Size
		}
	}
	taskSize := uint64(taskBytes)
	workers := inputBytes / taskSize
	if inputBytes%taskSize != 0 {
		workers++
	}
	if workers == 0 {
		return 1
	}
	if workers >= uint64(maximum) {
		return maximum
	}
	return int(workers)
}

// runCompactionTasks 并行扫描每个 key 区间，并在区间内保留最新版本。
// 每个任务写入独立结果槽，因此返回顺序始终与 ranges 一致。
// 只有目标是最底层时才删除墓碑并执行 Retention/MaxSizeBytes；上层必须保留墓碑，防止旧值复活。
func runCompactionTasks(
	tables []*SStable,
	indexes []int,
	ranges []compactionRange,
	bottomLevel bool,
	options Options,
	now func() time.Time,
) ([]compactionTaskResult, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	taskNow := now
	if bottomLevel && options.Retention > 0 {
		fixedNow := now()
		taskNow = func() time.Time { return fixedNow }
	}

	results, err := runCompactionRanges(ranges, func(keyRange compactionRange) (compactionTaskResult, error) {
		result := compactionTaskResult{keyRange: keyRange}
		latest := make(map[string]Record)
		for _, tableIndex := range indexes {
			if tableIndex < 0 || tableIndex >= len(tables) || tables[tableIndex] == nil {
				return result, ErrInvalidSSTable
			}
			// Compaction 是顺序大扫描，不进入 Block Cache，避免淘汰前台读取的热点 Block。
			records, scanErr := tables[tableIndex].scan(keyRange.startKey, keyRange.endKey, false)
			if scanErr != nil {
				return result, scanErr
			}
			result.inputRecords += len(records)
			for _, record := range records {
				latest[record.Key] = record
			}
		}

		// 容量保留必须在汇总所有范围后全局应用，不能给每个子任务单独分配完整额度。
		taskOptions := options
		taskOptions.MaxSizeBytes = 0
		result.records = compactLevelRecords(latest, bottomLevel, taskOptions, taskNow)
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	if bottomLevel && options.MaxSizeBytes > 0 {
		applyGlobalSizeRetention(results, options.MaxSizeBytes)
	}
	return results, nil
}

func runCompactionRanges(
	ranges []compactionRange,
	run func(compactionRange) (compactionTaskResult, error),
) ([]compactionTaskResult, error) {
	// ranges 由 planCompactionRanges 保证互不重叠；调用方不能传入相交范围。
	results := make([]compactionTaskResult, len(ranges))
	errs := make([]error, len(ranges))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(ranges))
	for index, keyRange := range ranges {
		go func() {
			defer waitGroup.Done()
			results[index], errs[index] = run(keyRange)
		}()
	}
	waitGroup.Wait()
	return results, errors.Join(errs...)
}

func applyGlobalSizeRetention(results []compactionTaskResult, maxSizeBytes int64) {
	var records []Record
	for _, result := range results {
		records = append(records, result.records...)
	}
	retained := enforceSizeRetention(records, maxSizeBytes)
	for index := range results {
		results[index].records = scanSortedRecords(
			retained,
			results[index].keyRange.startKey,
			results[index].keyRange.endKey,
		)
	}
}

// writeCompactionOutputs 为每个非空范围并行生成一张 SSTable。
// 只有全部文件都写成功，调用方才可以把这些输出发布到 Manifest。
// 任一写入失败时会关闭并删除本次已成功创建的文件，但不会删除 table=nil 对应的已有路径。
func writeCompactionOutputs(
	dir string,
	firstFileID uint64,
	results []compactionTaskResult,
	cache *BlockCache,
) ([]compactionOutput, error) {
	return writeCompactionOutputsWithWriter(dir, firstFileID, results, cache, WriteSStable)
}

func writeCompactionOutputsWithWriter(
	dir string,
	firstFileID uint64,
	results []compactionTaskResult,
	cache *BlockCache,
	write func(string, []Record) (*SStable, error),
) ([]compactionOutput, error) {
	outputs := make([]compactionOutput, 0, len(results))
	for _, result := range results {
		if len(result.records) == 0 {
			continue
		}
		outputs = append(outputs, compactionOutput{
			keyRange: result.keyRange,
			records:  result.records,
			path:     sstablePath(dir, firstFileID+uint64(len(outputs))),
		})
	}

	errs := make([]error, len(outputs))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(outputs))
	for index := range outputs {
		go func() {
			defer waitGroup.Done()
			outputs[index].table, errs[index] = write(outputs[index].path, outputs[index].records)
			if errs[index] == nil && outputs[index].table == nil {
				errs[index] = ErrInvalidSSTable
			}
			if errs[index] == nil {
				outputs[index].table.SetBlockCache(cache)
			}
		}()
	}
	waitGroup.Wait()
	if err := errors.Join(errs...); err != nil {
		cleanupCompactionOutputs(outputs)
		return nil, err
	}
	return outputs, nil
}

func cleanupCompactionOutputs(outputs []compactionOutput) {
	for _, output := range outputs {
		// table=nil 表示目标文件并非本次任务成功创建，不能按路径误删已有文件。
		cleanupCompactionOutput(output.table, output.path)
	}
}
