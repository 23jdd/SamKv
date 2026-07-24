package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStressRunnerVerifiesKVAndLogWorkloads(t *testing.T) {
	for _, mode := range []string{"kv", "logs"} {
		t.Run(mode, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{
				"-mode", mode,
				"-count", "100",
				"-concurrency", "4",
				"-value-bytes", "32",
			}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("run error=%v stderr=%s", err, stderr.String())
			}
			var report stressReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Mode != mode || report.Records != 100 || !report.Verified ||
				report.OperationsPerSec <= 0 || report.SSTables == 0 {
				t.Fatalf("report = %#v", report)
			}
		})
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
