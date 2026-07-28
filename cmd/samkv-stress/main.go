package main

// 本文件运行可配置的日志并发写入、Checkpoint、重启读取和完整性验证压力测试。
// 结果只适用于报告中的 WAL 同步策略、数据可压缩性、硬件与并发度，不能直接代表生产吞吐。

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

type stressConfig struct {
	dir            string
	mode           string
	count          int
	concurrency    int
	valueBytes     int
	payloadPattern string
	strict         bool
	verify         bool
}

type stressReport struct {
	Directory             string                `json:"directory"`
	Mode                  string                `json:"mode"`
	Records               int                   `json:"records"`
	Concurrency           int                   `json:"concurrency"`
	ValueBytes            int                   `json:"value_bytes"`
	PayloadPattern        string                `json:"payload_pattern"`
	WALSyncPolicy         string                `json:"wal_sync_policy"`
	WriteDuration         time.Duration         `json:"write_duration"`
	WriteOperationsPerSec float64               `json:"write_operations_per_second"`
	PayloadMiBPerSec      float64               `json:"payload_mib_per_second"`
	CheckpointDuration    time.Duration         `json:"checkpoint_duration"`
	ReopenDuration        time.Duration         `json:"reopen_duration"`
	VerifyDuration        time.Duration         `json:"verify_duration"`
	Duration              time.Duration         `json:"duration"`
	OperationsPerSec      float64               `json:"operations_per_second"`
	Verified              bool                  `json:"verified"`
	WALBytes              int64                 `json:"wal_bytes"`
	SSTableBytes          int64                 `json:"sstable_bytes"`
	SSTables              int                   `json:"sstables"`
	BlockCache            store.BlockCacheStats `json:"block_cache"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run 要求指定目录为空；未指定 -dir 时使用临时目录并在报告写出后删除。
// -strict 对每次 WAL 写入 fsync，-verify 把 Checkpoint/重启/逐条读取计入总耗时但不计入纯写入速率。
func run(args []string, stdout, stderr io.Writer) error {
	config, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	dir, cleanup, err := prepareDirectory(config.dir)
	if err != nil {
		return err
	}
	defer cleanup()

	options := store.DefaultOptions()
	options.MemTableLimit = 16 * 1024 * 1024
	if config.strict {
		options.WALSyncPolicy = store.WALSyncEveryWrite
		options.WALSyncInterval = 0
	}
	database, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		return err
	}

	started := time.Now()
	baseTimestamp := time.Now().UTC()

	writeStarted := time.Now()
	phaseErr := runWrites(database, config, baseTimestamp)
	writeDuration := elapsedSince(writeStarted)

	var checkpointDuration time.Duration
	if phaseErr == nil {
		checkpointStarted := time.Now()
		_, phaseErr = database.Checkpoint()
		checkpointDuration = elapsedSince(checkpointStarted)
	}

	stats := database.Stats()
	closeErr := database.Close()
	if err := errors.Join(phaseErr, closeErr); err != nil {
		return err
	}

	var reopenDuration, verifyDuration time.Duration
	verified := false
	if config.verify {
		reopenStarted := time.Now()
		database, err = store.NewStoreManagerWithOptions(dir, options)
		reopenDuration = elapsedSince(reopenStarted)
		if err != nil {
			return err
		}

		verifyStarted := time.Now()
		verifyErr := verifyWrites(database, config, baseTimestamp)
		verifyDuration = elapsedSince(verifyStarted)
		verified = verifyErr == nil
		stats = database.Stats()
		closeErr = database.Close()
		if err := errors.Join(verifyErr, closeErr); err != nil {
			return err
		}
	}

	duration := elapsedSince(started)
	phaseDuration := writeDuration + checkpointDuration + reopenDuration + verifyDuration
	if duration < phaseDuration {
		duration = phaseDuration
	}
	payloadBytes := int64(config.count) * int64(config.valueBytes)
	report := stressReport{
		Directory:             dir,
		Mode:                  config.mode,
		Records:               config.count,
		Concurrency:           config.concurrency,
		ValueBytes:            config.valueBytes,
		PayloadPattern:        config.payloadPattern,
		WALSyncPolicy:         syncPolicyName(config.strict),
		WriteDuration:         writeDuration,
		WriteOperationsPerSec: operationsPerSecond(config.count, writeDuration),
		PayloadMiBPerSec:      mebibytesPerSecond(payloadBytes, writeDuration),
		CheckpointDuration:    checkpointDuration,
		ReopenDuration:        reopenDuration,
		VerifyDuration:        verifyDuration,
		Duration:              duration,
		OperationsPerSec:      operationsPerSecond(config.count, duration),
		Verified:              verified,
		WALBytes:              stats.WALBytes,
		SSTableBytes:          stats.SSTableBytes,
		SSTables:              stats.SSTables,
		BlockCache:            stats.BlockCache,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func syncPolicyName(strict bool) string {
	if strict {
		return "every-write"
	}
	return "interval"
}

// elapsedSince 保证已执行阶段至少报告 1ns。
// 某些 Windows 计时源在极短阶段可能返回 0；保留正值可避免速率字段被误报为零。
func elapsedSince(started time.Time) time.Duration {
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}
func operationsPerSecond(count int, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}

func mebibytesPerSecond(bytes int64, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(bytes) / (1024 * 1024) / duration.Seconds()
}

func parseConfig(args []string, output io.Writer) (stressConfig, error) {
	config := stressConfig{}
	flags := flag.NewFlagSet("samkv-stress", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&config.dir, "dir", "", "empty/new data directory; defaults to a temporary directory")
	flags.StringVar(&config.mode, "mode", "logs", "workload mode: logs")
	flags.IntVar(&config.count, "count", 100_000, "number of records")
	flags.IntVar(&config.concurrency, "concurrency", runtime.GOMAXPROCS(0), "writer goroutines")
	flags.IntVar(&config.valueBytes, "value-bytes", 256, "value or message bytes")
	flags.StringVar(&config.payloadPattern, "payload-pattern", "repeated", "payload pattern: repeated or random")
	flags.BoolVar(&config.strict, "strict", false, "fsync every write")
	flags.BoolVar(&config.verify, "verify", true, "read all records after checkpoint")
	if err := flags.Parse(args); err != nil {
		return stressConfig{}, err
	}
	if flags.NArg() != 0 {
		return stressConfig{}, errors.New("unexpected positional arguments")
	}
	if config.mode != "logs" {
		return stressConfig{}, errors.New("mode must be logs")
	}
	if config.count <= 0 || config.concurrency <= 0 || config.valueBytes < 0 {
		return stressConfig{}, errors.New("count and concurrency must be positive; value-bytes must not be negative")
	}
	if config.payloadPattern != "repeated" && config.payloadPattern != "random" {
		return stressConfig{}, errors.New("payload-pattern must be repeated or random")
	}
	return config, nil
}

func prepareDirectory(requested string) (string, func(), error) {
	if requested == "" {
		dir, err := os.MkdirTemp("", "samkv-stress-")
		if err != nil {
			return "", func() {}, err
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}
	dir, err := filepath.Abs(requested)
	if err != nil {
		return "", func() {}, err
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return "", func() {}, errors.New("stress directory must be empty")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", func() {}, err
	}
	return dir, func() {}, nil
}

// stressPayload 生成可复现的高压缩或低压缩测试数据，避免随机源本身进入计时阶段。
func stressPayload(config stressConfig) []byte {
	if config.payloadPattern == "repeated" {
		return bytes.Repeat([]byte("x"), config.valueBytes)
	}
	payload := make([]byte, config.valueBytes)
	random := rand.New(rand.NewSource(1))
	_, _ = random.Read(payload)
	return payload
}

func runWrites(database *store.StoreManager, config stressConfig, base time.Time) error {
	value := stressPayload(config)
	labels := []utils.Label{{Name: "app", Value: "stress"}}
	var next atomic.Int64
	var firstErr error
	var once sync.Once
	var wait sync.WaitGroup
	for worker := 0; worker < config.concurrency; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= config.count {
					return
				}
				var err error
			_, err = database.WriteLog(store.LogEntry{
				Timestamp: base.Add(time.Duration(index) * time.Nanosecond),
				Labels:    labels,
				Message:   value,
			})
				if err != nil {
					once.Do(func() { firstErr = err })
					return
				}
			}
		}()
	}
	wait.Wait()
	return firstErr
}

func verifyWrites(database *store.StoreManager, config stressConfig, base time.Time) error {
	logs, err := database.Query(
		base,
		base.Add(time.Duration(config.count)*time.Nanosecond),
		[]utils.Label{{Name: "app", Value: "stress"}},
	)
	if err != nil {
		return err
	}
	if len(logs) != config.count {
		return fmt.Errorf("verified %d logs, want %d", len(logs), config.count)
	}
	return nil
}
