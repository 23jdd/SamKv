package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23jdd/SamKv/pkg/store"
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
	if err := database.Put("key", "value"); err != nil {
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
	if value, ok := restored.Get("key"); !ok || value != "value" {
		t.Fatalf("Get(key) = %q, %v", value, ok)
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
