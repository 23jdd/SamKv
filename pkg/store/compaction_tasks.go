package store

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

// runCompactionTasks 并行扫描每个 key 区间，并在区间内保留最新版本。
// 每个任务写入独立结果槽，因此返回顺序始终与 ranges 一致。
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
