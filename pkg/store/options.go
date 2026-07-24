package store

import (
	"errors"
	"time"

	"github.com/23jdd/SamKv/pkg/wal"
)

const (
	DefaultMemTableLimit       = 4 * 1024 * 1024
	DefaultCompactionThreshold = 4
	DefaultBlockCacheBytes     = 64 * 1024 * 1024
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
	// Retention 是日志保留时长，仅在 Compaction 时淘汰过期记录，0 表示永久保留。
	Retention time.Duration
	// MaxSizeBytes 是 Compaction 后允许保留的近似数据量，0 表示不限制容量。
	MaxSizeBytes int64
	// BlockCacheBytes 是共享 SSTable Block Cache 的容量，0 表示禁用。
	BlockCacheBytes int64
	// WALSyncPolicy 决定写入返回前是否必须完成 fsync。
	WALSyncPolicy WALSyncPolicy
	// WALSyncInterval 是周期同步模式的刷盘间隔；0 使用 WAL 默认值。
	WALSyncInterval time.Duration
}

// DefaultOptions 返回适合本地日志存储的默认配置。
func DefaultOptions() Options {
	return Options{
		MemTableLimit:       DefaultMemTableLimit,
		AutoCheckpoint:      true,
		CompactionThreshold: DefaultCompactionThreshold,
		BlockCacheBytes:     DefaultBlockCacheBytes,
		WALSyncPolicy:       WALSyncInterval,
		WALSyncInterval:     wal.FlushInterval,
	}
}

func validateOptions(options Options) error {
	if options.MemTableLimit < 0 ||
		options.CompactionThreshold < 0 ||
		options.Retention < 0 ||
		options.MaxSizeBytes < 0 ||
		options.BlockCacheBytes < 0 ||
		options.WALSyncInterval < 0 {
		return ErrInvalidOptions
	}
	if options.WALSyncPolicy != WALSyncInterval && options.WALSyncPolicy != WALSyncEveryWrite {
		return ErrInvalidOptions
	}
	return nil
}
