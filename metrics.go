package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/gin-gonic/gin"
)

const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// MetricsStore 定义指标接口读取 Store 状态所需的能力。
type MetricsStore interface {
	Stats() store.Stats
}

func registerMetricsRoute(router *gin.Engine, database MetricsStore) {
	router.GET("/metrics", func(c *gin.Context) {
		c.Data(http.StatusOK, prometheusContentType, []byte(formatPrometheusMetrics(database.Stats())))
	})
}

func formatPrometheusMetrics(stats store.Stats) string {
	var output strings.Builder
	writeMetric := func(name, help, metricType string, value any) {
		fmt.Fprintf(&output, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&output, "# TYPE %s %s\n", name, metricType)
		fmt.Fprintf(&output, "%s %v\n", name, value)
	}
	writeMetric("samkv_write_operations_total", "Total Store write operations.", "counter", stats.WriteOperations)
	writeMetric("samkv_read_operations_total", "Total Store read operations.", "counter", stats.ReadOperations)
	writeMetric("samkv_checkpoints_total", "Total completed checkpoints.", "counter", stats.Checkpoints)
	writeMetric("samkv_compactions_total", "Total started compactions.", "counter", stats.Compactions)
	writeMetric("samkv_active_memtable_entries", "Entries in the active MemTable.", "gauge", stats.ActiveMemTableEntries)
	writeMetric("samkv_active_memtable_bytes", "Approximate bytes in the active MemTable.", "gauge", stats.ActiveMemTableBytes)
	writeMetric("samkv_immutable_memtables", "Immutable MemTables waiting for flush.", "gauge", stats.ImmutableMemTables)
	writeMetric("samkv_immutable_entries", "Entries in immutable MemTables.", "gauge", stats.ImmutableEntries)
	writeMetric("samkv_sstables", "Published SSTable count.", "gauge", stats.SSTables)
	writeMetric("samkv_sstable_records", "Records described by the Manifest.", "gauge", stats.SSTableRecords)
	writeMetric("samkv_wal_bytes", "Current WAL file bytes.", "gauge", stats.WALBytes)
	writeMetric("samkv_sstable_bytes", "Current SSTable file bytes.", "gauge", stats.SSTableBytes)
	writeMetric("samkv_block_cache_hits_total", "SSTable Block Cache hits.", "counter", stats.BlockCache.Hits)
	writeMetric("samkv_block_cache_misses_total", "SSTable Block Cache misses.", "counter", stats.BlockCache.Misses)
	writeMetric("samkv_block_cache_evictions_total", "SSTable Block Cache evictions.", "counter", stats.BlockCache.Evictions)
	writeMetric("samkv_block_cache_entries", "Current SSTable Block Cache entries.", "gauge", stats.BlockCache.Entries)
	writeMetric("samkv_block_cache_bytes", "Current SSTable Block Cache bytes.", "gauge", stats.BlockCache.Bytes)
	backgroundError := 0
	if stats.BackgroundError != nil {
		backgroundError = 1
	}
	writeMetric("samkv_background_error", "Whether background maintenance has failed.", "gauge", backgroundError)

	levels := make([]int, 0, len(stats.LevelTables))
	for level := range stats.LevelTables {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	output.WriteString("# HELP samkv_level_sstables Published SSTables by level.\n")
	output.WriteString("# TYPE samkv_level_sstables gauge\n")
	for _, level := range levels {
		fmt.Fprintf(&output, "samkv_level_sstables{level=\"%d\"} %d\n", level, stats.LevelTables[level])
	}
	return output.String()
}
