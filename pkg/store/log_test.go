package store

import (
	"bytes"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

func TestWriteLogAndQueryAcrossMemoryAndSSTable(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{
			Timestamp: base,
			Labels:    []utils.Label{{Name: "level", Value: "ERROR"}, {Name: "app", Value: "nginx"}},
			Message:   []byte("nginx failed"),
		},
		{
			Timestamp: base.Add(time.Minute),
			Labels:    []utils.Label{{Name: "app", Value: "api"}, {Name: "level", Value: "ERROR"}},
			Message:   []byte("api failed"),
		},
		{
			Timestamp: base.Add(2 * time.Minute),
			Labels:    []utils.Label{{Name: "level", Value: "INFO"}, {Name: "app", Value: "nginx"}},
			Message:   []byte("nginx ready"),
		},
	}

	firstSequence, err := store.WriteLog(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteLog(entries[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	lastSequence, err := store.WriteLog(entries[2])
	if err != nil {
		t.Fatal(err)
	}
	if firstSequence == 0 || lastSequence <= firstSequence {
		t.Fatalf("sequences = %d, %d", firstSequence, lastSequence)
	}

	got, err := store.Query(base, base.Add(2*time.Minute), []utils.Label{{Name: "app", Value: "nginx"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Query(app=nginx) returned %d entries: %#v", len(got), got)
	}
	if !bytes.Equal(got[0].Message, []byte("nginx failed")) || !bytes.Equal(got[1].Message, []byte("nginx ready")) {
		t.Fatalf("Query messages = %q, %q", got[0].Message, got[1].Message)
	}

	errorLogs, err := store.Query(base, base.Add(2*time.Minute), []utils.Label{
		{Name: "app", Value: "nginx"},
		{Name: "level", Value: "ERROR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(errorLogs) != 1 || !bytes.Equal(errorLogs[0].Message, []byte("nginx failed")) {
		t.Fatalf("Query nginx errors = %#v", errorLogs)
	}
}

func TestLogSequenceContinuesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreManger(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	entry := LogEntry{
		Timestamp: time.Unix(100, 0).UTC(),
		Labels:    []utils.Label{{Name: "app", Value: "same"}},
		Message:   []byte("first"),
	}
	first, err := store.WriteLog(entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManger(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, err := reopened.WriteLog(LogEntry{
		Timestamp: entry.Timestamp,
		Labels:    entry.Labels,
		Message:   []byte("second"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second <= first {
		t.Fatalf("sequence after restart = %d, want > %d", second, first)
	}

	got, err := reopened.Query(entry.Timestamp, entry.Timestamp, entry.Labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Query() returned %d entries, want 2", len(got))
	}
}

func TestWriteLogsUsesOneBatchAndKeepsSequenceOrder(t *testing.T) {
	options := DefaultOptions()
	options.AutoCheckpoint = false
	store, err := NewStoreMangerWithOptions(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Unix(2_000, 0).UTC()
	labels := []utils.Label{{Name: "app", Value: "batch"}}
	sequences, err := store.WriteLogs([]LogEntry{
		{Timestamp: base, Labels: labels, Message: []byte("one")},
		{Timestamp: base.Add(time.Second), Labels: labels, Message: []byte("two")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != 2 || sequences[0] == 0 || sequences[1] <= sequences[0] {
		t.Fatalf("WriteLogs sequences = %#v", sequences)
	}
	got, err := store.Query(base, base.Add(time.Second), labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got[0].Message) != "one" || string(got[1].Message) != "two" {
		t.Fatalf("Query batch logs = %#v", got)
	}
	if stats := store.Stats(); stats.WriteOperations != 2 {
		t.Fatalf("WriteOperations = %d, want 2", stats.WriteOperations)
	}
}
