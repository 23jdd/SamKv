package store

// 本文件提供 Put/Get/WriteLog/Query 等热路径基准；基准结果依赖磁盘与 WAL 同步策略。

import (
	"fmt"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

func benchPut(b *testing.B, st *StoreManger, key, value string) {
	b.Helper()
	if err := st.WriteBatch(NewBatch().Put(key, value)); err != nil {
		b.Fatal(err)
	}
}

func benchGet(b *testing.B, st *StoreManger, key string) (string, bool) {
	b.Helper()
	st.mu.RLock()
	defer st.mu.RUnlock()

	if entry, ok := st.mem.table.Get(key); ok {
		if entry.Deleted {
			return "", false
		}
		return entry.Value, true
	}
	for i := len(st.immutables) - 1; i >= 0; i-- {
		immutable := st.immutables[i]
		if entry, ok := immutable.table.Get(key); ok {
			if entry.Deleted {
				return "", false
			}
			return entry.Value, true
		}
	}
	for i := len(st.sstables) - 1; i >= 0; i-- {
		record, ok, err := st.sstables[i].GetRecord(key)
		if err != nil {
			b.Helper()
			b.Fatalf("SSTable GetRecord error: %v", err)
		}
		if !ok {
			continue
		}
		if record.Deleted {
			return "", false
		}
		return record.Val, true
	}
	return "", false
}

func BenchmarkStorePutSyncInterval(b *testing.B) {
	benchmarkStorePut(b, WALSyncInterval)
}

func BenchmarkStorePutSyncEveryWrite(b *testing.B) {
	benchmarkStorePut(b, WALSyncEveryWrite)
}

func benchmarkStorePut(b *testing.B, policy WALSyncPolicy) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.WALSyncPolicy = policy
	database, err := NewStoreManagerWithOptions(b.TempDir(), options)
	if err != nil {
		b.Fatal(err)
	}
	value := string(make([]byte, 256))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := database.WriteBatch(NewBatch().Put(fmt.Sprintf("key-%012d", i), value)); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkStoreGetFromMemTable(b *testing.B) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(b.TempDir(), options)
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	benchPut(b, database, "key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := benchGet(b, database, "key"); !ok {
			b.Fatal("key not found")
		}
	}
}

func BenchmarkStoreGetFromCachedSSTable(b *testing.B) {
	dir := b.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := database.WriteBatch(NewBatch().Put(fmt.Sprintf("key-%04d", i), "value")); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := database.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
	database, err = NewStoreManagerWithOptions(dir, options)
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	if _, ok := benchGet(b, database, "key-0500"); !ok {
		b.Fatal("key not found")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := benchGet(b, database, "key-0500"); !ok {
			b.Fatal("key not found")
		}
	}
}

func BenchmarkStoreLogQuery(b *testing.B) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(b.TempDir(), options)
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	base := time.Now().UTC()
	labels := []utils.Label{{Name: "app", Value: "benchmark"}}
	entries := make([]LogEntry, 1000)
	for i := range entries {
		entries[i] = LogEntry{
			Timestamp: base.Add(time.Duration(i) * time.Nanosecond),
			Labels:    labels,
			Message:   []byte("benchmark message"),
		}
	}
	if _, err := database.WriteLogs(entries); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := database.Query(base, base.Add(time.Second), labels); err != nil {
			b.Fatal(err)
		}
	}
}
