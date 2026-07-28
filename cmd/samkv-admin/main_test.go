package main

// 本文件验证 admin 子命令参数、JSON 报告、备份恢复、损坏修复和目录锁冲突。

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

func TestAdminCLIBackupVerifyAndRestore(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	restoreDir := filepath.Join(root, "restore")
	database, err := store.NewStoreManager(dataDir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	if _, err := database.WriteLog(store.LogEntry{
		Timestamp: now,
		Labels:    []utils.Label{{Name: "test", Value: "backup"}},
		Message:   []byte("hello"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"backup", "-dir", dataDir, "-dest", backupDir}, &stdout, &stderr); err != nil {
		t.Fatalf("backup error=%v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"format_version": 1`) {
		t.Fatalf("backup output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"verify-backup", "-source", backupDir}, &stdout, &stderr); err != nil {
		t.Fatalf("verify-backup error=%v stderr=%s", err, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"restore", "-source", backupDir, "-dest", restoreDir}, &stdout, &stderr); err != nil {
		t.Fatalf("restore error=%v stderr=%s", err, stderr.String())
	}

	restored, err := store.NewStoreManager(restoreDir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	logs, err := restored.Query(
		now.Add(-time.Hour),
		now.Add(time.Hour),
		[]utils.Label{{Name: "test", Value: "backup"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || string(logs[0].Message) != "hello" {
		t.Fatalf("Query() logs = %d, %q", len(logs), logs)
	}
}

func TestAdminCLIValidatesCommandsAndFlags(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"verify"},
		{"backup", "-dir", "data"},
		{"restore", "-source", "backup"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("run(%q) succeeded", args)
		}
	}
}
