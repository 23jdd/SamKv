package wal

// 本文件定义 WAL 缓冲与 fsync 策略配置。
// 使用 DefaultOptions 起步，只覆盖需要调整的字段，避免遗漏有效的同步间隔。

import (
	"errors"
	"time"
)

// SyncPolicy 控制 AppendLog 返回前 WAL 数据需要达到的持久化程度。
type SyncPolicy uint8

const (
	// SyncInterval 由后台任务按固定间隔执行 fsync。
	// 写入返回时数据可能仍在操作系统页缓存中，崩溃时可能丢失最近一个同步周期的数据。
	SyncInterval SyncPolicy = iota
	// SyncEveryWrite 要求 AppendLog 在返回前完成 fsync。
	SyncEveryWrite
)

// ErrInvalidOptions 表示缓冲大小、同步策略或同步间隔组合无效。
var ErrInvalidOptions = errors.New("wal: invalid options")

// Options 控制 WAL 的缓冲容量和持久性策略。
type Options struct {
	// BufferSize 是周期模式的内存缓冲容量，必须大于 0；它不是单条记录大小上限。
	BufferSize int
	// SyncPolicy 决定 AppendLog/AppendRecord 返回前是否完成 fsync。
	SyncPolicy SyncPolicy
	// SyncInterval 仅供 SyncInterval 策略使用，必须大于 0；严格模式会忽略它。
	SyncInterval time.Duration
}

// DefaultOptions 返回 64 KiB 缓冲区和 50 ms 周期同步配置。
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
