package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

func TestBackupVerifyAndRestoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	restoreDir := filepath.Join(root, "restored")
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	database, err := NewStoreManagerWithOptions(dataDir, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put("config", "enabled"); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Add(-time.Minute)
	labels := []utils.Label{{Name: "app", Value: "api"}}
	if _, err := database.WriteLog(LogEntry{Timestamp: timestamp, Labels: labels, Message: []byte("request failed")}); err != nil {
		t.Fatal(err)
	}

	metadata, err := database.Backup(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.FormatVersion != CurrentBackupVersion || len(metadata.Files) < 3 {
		t.Fatalf("backup metadata = %#v", metadata)
	}
	if _, err := VerifyBackup(backupDir); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(backupDir, restoreDir); err != nil {
		t.Fatal(err)
	}

	restored, err := NewStoreManagerWithOptions(restoreDir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if value, ok := restored.Get("config"); !ok || value != "enabled" {
		t.Fatalf("Get(config) = %q, %v", value, ok)
	}
	logs, err := restored.Query(timestamp.Add(-time.Second), timestamp.Add(time.Second), labels)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || string(logs[0].Message) != "request failed" {
		t.Fatalf("restored logs = %#v", logs)
	}
}

func TestVerifyBackupRejectsTamperedFile(t *testing.T) {
	root := t.TempDir()
	database, err := NewStoreManager(filepath.Join(root, "data"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put("key", "value"); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, "backup")
	metadata, err := database.Backup(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var tablePath string
	for _, file := range metadata.Files {
		if strings.HasSuffix(file.Name, ".sst") {
			tablePath = filepath.Join(backupDir, file.Name)
			break
		}
	}
	if tablePath == "" {
		t.Fatal("backup contains no SSTable")
	}
	flipFileByte(t, tablePath, 0)
	if _, err := VerifyBackup(backupDir); err == nil {
		t.Fatal("VerifyBackup() accepted tampered SSTable")
	}
}

func TestBackupRejectsDestinationInsideDataDirectory(t *testing.T) {
	dir := t.TempDir()
	database, err := NewStoreManager(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Backup(filepath.Join(dir, "backup")); err == nil {
		t.Fatal("Backup() accepted destination inside data directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "backup")); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup path state: %v", err)
	}
}
