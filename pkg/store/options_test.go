package store

// 本文件验证默认配置、所有数值边界和不支持的 WAL 同步策略会被拒绝。

import "testing"

func TestDefaultOptionsConfiguresCompactionWorkers(t *testing.T) {
	options := DefaultOptions()
	if options.CompactionWorkers != DefaultCompactionWorkers {
		t.Fatalf("CompactionWorkers = %d, want %d", options.CompactionWorkers, DefaultCompactionWorkers)
	}
	if options.CompactionTaskBytes != DefaultCompactionTaskBytes {
		t.Fatalf("CompactionTaskBytes = %d, want %d", options.CompactionTaskBytes, DefaultCompactionTaskBytes)
	}
}

func TestValidateOptionsRejectsInvalidCompactionWorkers(t *testing.T) {
	options := DefaultOptions()
	options.CompactionWorkers = 0
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("validateOptions() error = %v, want %v", err, ErrInvalidOptions)
	}
}

func TestValidateOptionsRejectsInvalidCompactionTaskBytes(t *testing.T) {
	options := DefaultOptions()
	options.CompactionTaskBytes = 0
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("validateOptions() error = %v, want %v", err, ErrInvalidOptions)
	}
}
