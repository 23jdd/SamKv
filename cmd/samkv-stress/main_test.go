package main

import (
	"bytes"
	"encoding/json"
	"testing"
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
				report.WALSyncPolicy != "interval" || report.SSTables == 0 {
				t.Fatalf("report = %#v", report)
			}
			if report.WriteDuration <= 0 || report.WriteOperationsPerSec <= 0 ||
				report.PayloadMiBPerSec <= 0 || report.CheckpointDuration <= 0 ||
				report.ReopenDuration <= 0 || report.VerifyDuration <= 0 ||
				report.Duration <= 0 || report.OperationsPerSec <= 0 {
				t.Fatalf("phase metrics = %#v", report)
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
		"-strict",
	)
	if report.WALSyncPolicy != "every-write" || !report.Verified ||
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
	if report.WriteDuration <= 0 || report.CheckpointDuration <= 0 {
		t.Fatalf("phase metrics = %#v", report)
	}
}

func TestStressRunnerRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"-mode", "unknown"},
		{"-count", "0"},
		{"-concurrency", "0"},
		{"-value-bytes", "-1"},
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
