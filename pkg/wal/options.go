package wal

import (
	"errors"
	"time"
)

// SyncPolicy 控制 AppendLog 返回前 WAL 数据需要达到的持久化程度。
type SyncPolicy uint8

const (
	// SyncInterval 由后台任务按固定间隔执行 fsync。
	// 写入返回时数据可能仍在操作系统页缓存中，崩溃时可能丢失最近一个同步周期的数据。?
	SyncInterval SyncPolicy = iota
	// SyncEveryWrite 要求 AppendLog 在返回前完成 fsync。
	SyncEveryWrite
)

var ErrInvalidOptions = errors.New("wal: invalid options")

// Options 控制 WAL 的缓冲容量和持久性策略。
type Options struct {
	BufferSize   int
	SyncPolicy   SyncPolicy
	SyncInterval time.Duration
}

// DefaultOptions 返回 4 KiB 缓冲区和 50 ms 周期同步配置。
func DefaultOptions() Options {
	return Options{
		BufferSize:   DefaultSize,
		SyncPolicy:   SyncInterval,
		SyncInterval: FlushInterval,
	}
}

func validateOptions(options Options) error {
	if options.BufferSize <= 0 {
		return ErrInvalidOptions
	}
	if options.SyncPolicy != SyncInterval && options.SyncPolicy != SyncEveryWrite {
		return ErrInvalidOptions
	}
	if options.SyncPolicy == SyncInterval && options.SyncInterval <= 0 {
		return ErrInvalidOptions
	}
	return nil
}
