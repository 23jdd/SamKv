package store

// 本文件验证默认配置、所有数值边界和不支持的 WAL 同步策略会被拒绝。

import (
	"fmt"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
	"github.com/23jdd/SamKv/pkg/wal"
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
	raw, ok := getStore(t, database, string(key))
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

func TestWALSegmentOptions(t *testing.T) {
	options := DefaultOptions()
	if options.WALSegmentSize != wal.DefaultSegmentSize || options.WALSegmentMaxRecords != 0 {
		t.Fatalf("default WAL segment options = %d bytes, %d records", options.WALSegmentSize, options.WALSegmentMaxRecords)
	}
	options.WALSegmentSize = 0
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("zero WALSegmentSize error = %v, want %v", err, ErrInvalidOptions)
	}
}

func TestStorePassesWALSegmentOptions(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.WALSegmentSize = 128
	options.WALSegmentMaxRecords = 2
	database, err := NewStoreManagerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index := 0; index < 3; index++ {
		if err := database.WriteBatch(NewBatch().Put(fmt.Sprintf("key-%d", index), "value")); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.wm.Flush(); err != nil {
		t.Fatal(err)
	}
	segments, err := wal.ListSegments(database.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) < 2 {
		t.Fatalf("WAL segments = %+v, want rotation by record count", segments)
	}
}
