package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestStressRunnerVerifiesPersistedKVAndLogWorkloads(t *testing.T) {
	for _, mode := range []string{"kv", "logs"} {
		t.Run(mode, func(t *testing.T) {
			report := runStressReport(t,
				"-mode", mode,
				"-count", "100",
				"-concurrency", "4",
				"-value-bytes", "32",
			)
			if report.Mode != mode || report.Records != 100 || !report.Verified ||
				report.WALSyncPolicy != "interval" || report.PayloadPattern != "repeated" || report.SSTables == 0 {
				t.Fatalf("report = %#v", report)
			}
			if report.CheckpointDuration <= 0 || report.ReopenDuration <= 0 ||
				report.VerifyDuration <= 0 || report.Duration <= 0 ||
				report.OperationsPerSec <= 0 {
				t.Fatalf("phase metrics = %#v", report)
			}
			if report.WriteDuration > 0 &&
				(report.WriteOperationsPerSec <= 0 || report.PayloadMiBPerSec <= 0) {
				t.Fatalf("write metrics = %#v", report)
			}
			if report.Duration < report.WriteDuration+report.CheckpointDuration+
				report.ReopenDuration+report.VerifyDuration {
				t.Fatalf("total duration does not include all phases: %#v", report)
			}
		})
	}
}

func TestStressRunnerReportsEveryWriteDurability(t *testing.T) {
	report := runStressReport(t,
		"-mode", "logs",
		"-count", "20",
		"-concurrency", "2",
		"-value-bytes", "16",
		"-payload-pattern", "random",
		"-strict",
	)
	if report.WALSyncPolicy != "every-write" || report.PayloadPattern != "random" || !report.Verified ||
		report.WriteOperationsPerSec <= 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestStressRunnerCanSkipReopenVerification(t *testing.T) {
	report := runStressReport(t,
		"-mode", "kv",
		"-count", "100",
		"-concurrency", "2",
		"-value-bytes", "16",
		"-verify=false",
	)
	if report.Verified || report.ReopenDuration != 0 || report.VerifyDuration != 0 {
		t.Fatalf("report = %#v", report)
	}
	if report.CheckpointDuration <= 0 {
		t.Fatalf("phase metrics = %#v", report)
	}
}

func TestStressRateCalculations(t *testing.T) {
	if got := operationsPerSecond(500, time.Second); got != 500 {
		t.Fatalf("operationsPerSecond = %f, want 500", got)
	}
	if got := mebibytesPerSecond(1024*1024, time.Second); got != 1 {
		t.Fatalf("mebibytesPerSecond = %f, want 1", got)
	}
	if operationsPerSecond(1, 0) != 0 || mebibytesPerSecond(1, 0) != 0 {
		t.Fatal("zero duration must produce a zero rate")
	}
}
func TestStressPayloadPatternsAreDeterministic(t *testing.T) {
	repeated := stressPayload(stressConfig{valueBytes: 64, payloadPattern: "repeated"})
	randomFirst := stressPayload(stressConfig{valueBytes: 64, payloadPattern: "random"})
	randomSecond := stressPayload(stressConfig{valueBytes: 64, payloadPattern: "random"})
	if !bytes.Equal(repeated, bytes.Repeat([]byte("x"), 64)) {
		t.Fatal("repeated payload is not compressible test data")
	}
	if bytes.Equal(repeated, randomFirst) || !bytes.Equal(randomFirst, randomSecond) {
		t.Fatal("random payload must be deterministic and distinct")
	}
}
func TestStressRunnerRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"-mode", "unknown"},
		{"-count", "0"},
		{"-concurrency", "0"},
		{"-value-bytes", "-1"},
		{"-payload-pattern", "unknown"},
		{"unexpected"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("run(%q) succeeded", args)
		}
	}
}

func runStressReport(t *testing.T, args ...string) stressReport {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run error=%v stderr=%s", err, stderr.String())
	}
	var report stressReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}
