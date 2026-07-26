package store

// 本文件定义 Store 配置、默认容量和 WAL 持久性策略。
// 应从 DefaultOptions 开始按需覆盖；Options 的完整零值不是有效配置。

import (
	"errors"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
	"github.com/23jdd/SamKv/pkg/wal"
)

const (
	DefaultMemTableLimit                        = 4 * 1024 * 1024
	DefaultCompactionThreshold                  = 4
	DefaultCompactionWorkers                    = 4
	DefaultCompactionTaskBytes                  = 8 * 1024 * 1024
	DefaultCompactionRateLimitBytesPerSec int64 = 64 * 1024 * 1024
	DefaultBlockCacheBytes                      = 64 * 1024 * 1024
	DefaultMaxLevels                            = 4
	DefaultLevelBaseSizeBytes                   = 64 * 1024 * 1024
	DefaultLevelSizeMultiplier                  = 10
)

// WALSyncPolicy 是 Store 对 WAL 持久性策略的公开别名。
type WALSyncPolicy = wal.SyncPolicy

const (
	WALSyncInterval   = wal.SyncInterval
	WALSyncEveryWrite = wal.SyncEveryWrite
)

var (
	ErrStoreClosed       = errors.New("store: closed")
	ErrInvalidOptions    = errors.New("store: invalid options")
	ErrBackgroundFailure = errors.New("store: background maintenance failed")
)

// Options 控制 Store 的内存阈值、后台刷盘、Compaction 和保留策略。
type Options struct {
	// MemTableLimit 是活动 MemTable 的近似字节上限，0 表示不按容量自动切换。
	MemTableLimit int
	// AutoCheckpoint 控制 MemTable 达到上限后是否自动切换并在后台刷盘。
	AutoCheckpoint bool
	// CompactionThreshold 是触发自动 Compaction 的 SSTable 数量，0 表示关闭自动 Compaction。
	CompactionThreshold int
	// CompactionWorkers 是单次分层 Compaction 最多并行执行的 key-range 子任务数。
	CompactionWorkers int
	// CompactionTaskBytes 是每增加一个并行子任务所需的近似输入字节数。
	CompactionTaskBytes int64
	// CompactionRateLimitBytesPerSec 是所有 Compaction 输出共享的字节速率上限，0 表示不限制。
	CompactionRateLimitBytesPerSec int64
	// MaxLevels 是 LSM 层数，至少为 2；L0 用于刷盘文件。
	MaxLevels int
	// LevelBaseSizeBytes 是 L1 触发下推到 L2 的近似字节阈值。
	LevelBaseSizeBytes int64
	// LevelSizeMultiplier 是相邻非零层容量阈值的倍率。
	LevelSizeMultiplier int
	// Retention 是日志保留时长，仅在 Compaction 时淘汰过期记录，0 表示永久保留。
	Retention time.Duration
	// MaxSizeBytes 是 Compaction 后允许保留的近似数据量，0 表示不限制容量。
	MaxSizeBytes int64
	// BlockCacheBytes 是共享 SSTable Block Cache 的容量，0 表示禁用。
	BlockCacheBytes int64
	// CompressionType 控制 WriteLog/WriteLogs 新写 Value 的压缩算法；旧 Value 仍按自身算法编号读取。
	CompressionType utils.CompressionType
	// WALSyncPolicy 决定写入返回前是否必须完成 fsync。
	WALSyncPolicy WALSyncPolicy
	// WALSyncInterval 是周期同步模式的刷盘间隔；0 使用 WAL 默认值。
	WALSyncInterval time.Duration
	// WALSegmentSize 是 WAL segment 的近似字节轮转阈值，记录不会跨 segment 拆分。
	WALSegmentSize int64
	// WALSegmentMaxRecords 是每段最多物理记录数，0 表示只按 WALSegmentSize 轮转。
	WALSegmentMaxRecords uint64
}

// DefaultOptions 返回适合本地日志存储的默认配置。
// WALSyncInterval 吞吐更高，但进程或系统崩溃时可能丢失最后一个同步周期；需要 Put 返回即落盘时改用 WALSyncEveryWrite。
func DefaultOptions() Options {
	return Options{
		MemTableLimit:                  DefaultMemTableLimit,
		AutoCheckpoint:                 true,
		CompactionThreshold:            DefaultCompactionThreshold,
		CompactionWorkers:              DefaultCompactionWorkers,
		CompactionTaskBytes:            DefaultCompactionTaskBytes,
		CompactionRateLimitBytesPerSec: DefaultCompactionRateLimitBytesPerSec,
		MaxLevels:                      DefaultMaxLevels,
		LevelBaseSizeBytes:             DefaultLevelBaseSizeBytes,
		LevelSizeMultiplier:            DefaultLevelSizeMultiplier,
		BlockCacheBytes:                DefaultBlockCacheBytes,
		CompressionType:                utils.CompressionSnappy,
		WALSyncPolicy:                  WALSyncInterval,
		WALSyncInterval:                wal.FlushInterval,
		WALSegmentSize:                 wal.DefaultSegmentSize,
	}
}

func validateOptions(options Options) error {
	if options.MemTableLimit < 0 ||
		options.CompactionThreshold < 0 ||
		options.CompactionWorkers <= 0 ||
		options.CompactionTaskBytes <= 0 ||
		options.CompactionRateLimitBytesPerSec < 0 ||
		options.MaxLevels < 2 ||
		options.LevelBaseSizeBytes <= 0 ||
		options.LevelSizeMultiplier < 2 ||
		options.Retention < 0 ||
		options.MaxSizeBytes < 0 ||
		options.BlockCacheBytes < 0 ||
		options.WALSyncInterval < 0 ||
		options.WALSegmentSize <= 0 {
		return ErrInvalidOptions
	}
	if !options.CompressionType.Valid() {
		return ErrInvalidOptions
	}
	if options.WALSyncPolicy != WALSyncInterval && options.WALSyncPolicy != WALSyncEveryWrite {
		return ErrInvalidOptions
	}
	return nil
}
