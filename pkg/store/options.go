package store

import (
	"errors"
	"time"
)

const (
	DefaultMemTableLimit       = 4 * 1024 * 1024
	DefaultCompactionThreshold = 4
)

var (
	ErrStoreClosed       = errors.New("store: closed")
	ErrInvalidOptions    = errors.New("store: invalid options")
	ErrBackgroundFailure = errors.New("store: background maintenance failed")
)

// Options 控制 Store 的内存阈值、后台刷盘、Compaction 和保留策略。
type Options struct {
	MemTableLimit       int
	AutoCheckpoint      bool
	CompactionThreshold int
	Retention           time.Duration
	MaxSizeBytes        int64
}

// DefaultOptions 返回适合本地日志存储的默认配置。
func DefaultOptions() Options {
	return Options{
		MemTableLimit:       DefaultMemTableLimit,
		AutoCheckpoint:      true,
		CompactionThreshold: DefaultCompactionThreshold,
	}
}

func validateOptions(options Options) error {
	if options.MemTableLimit < 0 ||
		options.CompactionThreshold < 0 ||
		options.Retention < 0 ||
		options.MaxSizeBytes < 0 {
		return ErrInvalidOptions
	}
	return nil
}
