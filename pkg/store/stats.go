package store

// 本文件汇总 Store 的原子计数器、内存状态、磁盘占用、层级分布和 Block Cache 指标。
// Stats 是并发采样快照，不保证所有字段来自同一个绝对时刻。

import (
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/23jdd/SamKv/pkg/wal"
)

type statsCounters struct {
	writeOperations atomic.Uint64
	readOperations  atomic.Uint64
	checkpoints     atomic.Uint64
	compactions     atomic.Uint64
	compactionTasks atomic.Uint64
	compactionFiles atomic.Uint64
}

// Stats 是 Store 当前运行状态的只读快照。
type Stats struct {
	WriteOperations           uint64
	ReadOperations            uint64
	Checkpoints               uint64
	Compactions               uint64
	CompactionSubtasks        uint64
	CompactionOutputFiles     uint64
	ActiveMemTableEntries     int
	ActiveMemTableBytes       int
	ImmutableMemTables        int
	ImmutableEntries          int
	SSTables                  int
	SSTableRecords            uint64
	WALBytes                  int64
	WALSegments               int
	WALRecoverySkippedRecords int
	WALRecoveryTruncatedBytes int64
	SSTableBytes              int64
	LevelTables               map[int]int
	BlockCache                BlockCacheStats
	BackgroundError           error
}

// Stats 返回写入、查询、内存、WAL、SSTable 和后台错误统计。
// 文件可能在 os.Stat 前后被 Compaction 替换，因此磁盘字节数用于监控趋势，不作为事务性配额判断。
func (st *StoreManger) Stats() Stats {
	stats := Stats{
		WriteOperations:       st.stats.writeOperations.Load(),
		ReadOperations:        st.stats.readOperations.Load(),
		Checkpoints:           st.stats.checkpoints.Load(),
		Compactions:           st.stats.compactions.Load(),
		CompactionSubtasks:    st.stats.compactionTasks.Load(),
		CompactionOutputFiles: st.stats.compactionFiles.Load(),
		LevelTables:           make(map[int]int),
	}

	st.mu.RLock()
	stats.ActiveMemTableEntries = st.mem.Len()
	stats.ActiveMemTableBytes = st.mem.Size()
	stats.ImmutableMemTables = len(st.immutables)
	for _, immutable := range st.immutables {
		stats.ImmutableEntries += immutable.Len()
	}
	stats.SSTables = len(st.sstables)
	stats.BackgroundError = st.backgroundErr
	sstablePaths := make([]string, 0, len(st.manifest.SSTables))
	for _, entry := range st.manifest.SSTables {
		stats.SSTableRecords += entry.RecordCount
		stats.LevelTables[entry.Level]++
		sstablePaths = append(sstablePaths, filepath.Join(st.dir, entry.File))
	}
	stats.WALRecoverySkippedRecords = st.recoveryReport.SkippedRecords
	stats.WALRecoveryTruncatedBytes = st.recoveryReport.TruncatedBytes
	st.mu.RUnlock()

	if segments, err := wal.ListSegments(st.dir); err == nil {
		stats.WALSegments = len(segments)
		for _, segment := range segments {
			stats.WALBytes += segment.Size
		}
	}
	stats.BlockCache = st.blockCache.Stats()
	for _, path := range sstablePaths {
		if info, err := os.Stat(path); err == nil {
			stats.SSTableBytes += info.Size()
		}
	}
	return stats
}
