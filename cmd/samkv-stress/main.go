package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	dir         string
	mode        string
	count       int
	concurrency int
	valueBytes  int
	strict      bool
	verify      bool
}

type stressReport struct {
	Directory             string                `json:"directory"`
	Mode                  string                `json:"mode"`
	Records               int                   `json:"records"`
	Concurrency           int                   `json:"concurrency"`
	ValueBytes            int                   `json:"value_bytes"`
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
	writeDuration := time.Since(writeStarted)

	var checkpointDuration time.Duration
	if phaseErr == nil {
		checkpointStarted := time.Now()
		_, phaseErr = database.Checkpoint()
		checkpointDuration = time.Since(checkpointStarted)
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
		reopenDuration = time.Since(reopenStarted)
		if err != nil {
			return err
		}

		verifyStarted := time.Now()
		verifyErr := verifyWrites(database, config, baseTimestamp)
		verifyDuration = time.Since(verifyStarted)
		verified = verifyErr == nil
		stats = database.Stats()
		closeErr = database.Close()
		if err := errors.Join(verifyErr, closeErr); err != nil {
			return err
		}
	}

	duration := time.Since(started)
	payloadBytes := int64(config.count) * int64(config.valueBytes)
	report := stressReport{
		Directory:             dir,
		Mode:                  config.mode,
		Records:               config.count,
		Concurrency:           config.concurrency,
		ValueBytes:            config.valueBytes,
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
	flags.StringVar(&config.mode, "mode", "kv", "workload mode: kv or logs")
	flags.IntVar(&config.count, "count", 100_000, "number of records")
	flags.IntVar(&config.concurrency, "concurrency", runtime.GOMAXPROCS(0), "writer goroutines")
	flags.IntVar(&config.valueBytes, "value-bytes", 256, "value or message bytes")
	flags.BoolVar(&config.strict, "strict", false, "fsync every write")
	flags.BoolVar(&config.verify, "verify", true, "read all records after checkpoint")
	if err := flags.Parse(args); err != nil {
		return stressConfig{}, err
	}
	if flags.NArg() != 0 {
		return stressConfig{}, errors.New("unexpected positional arguments")
	}
	if config.mode != "kv" && config.mode != "logs" {
		return stressConfig{}, errors.New("mode must be kv or logs")
	}
	if config.count <= 0 || config.concurrency <= 0 || config.valueBytes < 0 {
		return stressConfig{}, errors.New("count and concurrency must be positive; value-bytes must not be negative")
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

func runWrites(database *store.StoreManager, config stressConfig, base time.Time) error {
	value := bytes.Repeat([]byte("x"), config.valueBytes)
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
				if config.mode == "kv" {
					err = database.Put(fmt.Sprintf("key-%012d", index), string(value))
				} else {
					_, err = database.WriteLog(store.LogEntry{
						Timestamp: base.Add(time.Duration(index) * time.Nanosecond),
						Labels:    labels,
						Message:   value,
					})
				}
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
	if config.mode == "logs" {
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
	for index := 0; index < config.count; index++ {
		key := fmt.Sprintf("key-%012d", index)
		value, ok := database.Get(key)
		if !ok || len(value) != config.valueBytes {
			return fmt.Errorf("verification failed for %s", key)
		}
	}
	return nil
}
