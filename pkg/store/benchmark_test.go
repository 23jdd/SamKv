package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

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
		if err := database.Put(fmt.Sprintf("key-%012d", i), value); err != nil {
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
	if err := database.Put("key", "value"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := database.Get("key"); !ok {
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
		if err := database.Put(fmt.Sprintf("key-%04d", i), "value"); err != nil {
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
	if _, ok := database.Get("key-0500"); !ok {
		b.Fatal("key not found")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := database.Get("key-0500"); !ok {
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
