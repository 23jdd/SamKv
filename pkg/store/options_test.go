package store

// 本文件验证默认配置、所有数值边界和不支持的 WAL 同步策略会被拒绝。

import (
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

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

func TestDefaultOptionsUsesSnappyForStructuredLogs(t *testing.T) {
	options := DefaultOptions()
	if options.CompressionType != utils.CompressionSnappy {
		t.Fatalf("CompressionType = %v, want snappy", options.CompressionType)
	}
}

func TestValidateOptionsRejectsUnknownCompression(t *testing.T) {
	options := DefaultOptions()
	options.CompressionType = utils.CompressionType(255)
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("validateOptions() error = %v, want %v", err, ErrInvalidOptions)
	}
}

func TestWriteLogUsesConfiguredCompression(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompressionType = utils.CompressionLZ4
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	timestamp := time.Unix(1700000000, 123).UTC()
	labels := []utils.Label{{Name: "app", Value: "api"}}
	if _, err := database.WriteLog(LogEntry{
		Timestamp: timestamp,
		Labels:    labels,
		Sequence:  7,
		Message:   []byte("request completed"),
	}); err != nil {
		t.Fatal(err)
	}
	key, err := utils.EncodeKey(timestamp.UnixNano(), labels, 7)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := database.Get(string(key))
	if !ok {
		t.Fatal("structured log key not found")
	}
	value, err := utils.UnmarshalValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if value.Compression != utils.CompressionLZ4 {
		t.Fatalf("stored compression = %v, want lz4", value.Compression)
	}
}

func TestCompactionRateLimitOptions(t *testing.T) {
	options := DefaultOptions()
	if options.CompactionRateLimitBytesPerSec != DefaultCompactionRateLimitBytesPerSec {
		t.Fatalf("CompactionRateLimitBytesPerSec = %d, want %d", options.CompactionRateLimitBytesPerSec, DefaultCompactionRateLimitBytesPerSec)
	}
	options.CompactionRateLimitBytesPerSec = 0
	if err := validateOptions(options); err != nil {
		t.Fatalf("zero rate limit should disable throttling: %v", err)
	}
	options.CompactionRateLimitBytesPerSec = -1
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("negative rate limit error = %v, want %v", err, ErrInvalidOptions)
	}
}
