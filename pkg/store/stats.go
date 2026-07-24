package store

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

type statsCounters struct {
	writeOperations atomic.Uint64
	readOperations  atomic.Uint64
	checkpoints     atomic.Uint64
	compactions     atomic.Uint64
}

// Stats 是 Store 当前运行状态的只读快照。
type Stats struct {
	WriteOperations       uint64
	ReadOperations        uint64
	Checkpoints           uint64
	Compactions           uint64
	ActiveMemTableEntries int
	ActiveMemTableBytes   int
	ImmutableMemTables    int
	ImmutableEntries      int
	SSTables              int
	SSTableRecords        uint64
	WALBytes              int64
	SSTableBytes          int64
	LevelTables           map[int]int
	BlockCache            BlockCacheStats
	BackgroundError       error
}

// Stats 返回写入、查询、内存、WAL、SSTable 和后台错误统计。
func (st *StoreManger) Stats() Stats {
	stats := Stats{
		WriteOperations: st.stats.writeOperations.Load(),
		ReadOperations:  st.stats.readOperations.Load(),
		Checkpoints:     st.stats.checkpoints.Load(),
		Compactions:     st.stats.compactions.Load(),
		LevelTables:     make(map[int]int),
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
	walPath := filepath.Join(st.dir, "wal.log")
	st.mu.RUnlock()

	if info, err := os.Stat(walPath); err == nil {
		stats.WALBytes = info.Size()
	}
	stats.BlockCache = st.blockCache.Stats()
	for _, path := range sstablePaths {
		if info, err := os.Stat(path); err == nil {
			stats.SSTableBytes += info.Size()
		}
	}
	return stats
}
