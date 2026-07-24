package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
)

func TestLoadReadsDurabilityCacheAndLevelSettings(t *testing.T) {
	t.Setenv("MemTableLimit", "8192")
	t.Setenv("AutoCheckpoint", "false")
	t.Setenv("CompactionThreshold", "3")
	t.Setenv("Retention", "2")
	t.Setenv("MaxSizeBytes", "1000")
	t.Setenv("BlockCacheBytes", "2000")
	t.Setenv("MaxLevels", "5")
	t.Setenv("LevelBaseSizeBytes", "3000")
	t.Setenv("LevelSizeMultiplier", "4")
	t.Setenv("WALSyncPolicy", "every-write")
	t.Setenv("WALSyncInterval", "3ms")

	options := Load()
	if options.MemTableLimit != 8192 ||
		options.AutoCheckpoint ||
		options.CompactionThreshold != 3 ||
		options.Retention != 2*time.Hour ||
		options.MaxSizeBytes != 1000 ||
		options.BlockCacheBytes != 2000 ||
		options.MaxLevels != 5 ||
		options.LevelBaseSizeBytes != 3000 ||
		options.LevelSizeMultiplier != 4 ||
		options.WALSyncPolicy != store.WALSyncEveryWrite ||
		options.WALSyncInterval != 3*time.Millisecond {
		t.Fatalf("Load() = %#v", options)
	}
}

func TestLoadEnvFileReadsCustomPath(t *testing.T) {
	unsetenvForTest(t, "MemTableLimit")
	unsetenvForTest(t, "Retention")
	path := filepath.Join(t.TempDir(), "custom.env")
	if err := os.WriteFile(path, []byte("MemTableLimit=2048\nRetention=6\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	options := LoadEnvFile(path)
	if options.MemTableLimit != 2048 || options.Retention != 6*time.Hour {
		t.Fatalf("LoadEnvFile() = %#v", options)
	}
}

func TestParseServerConfigAcceptsEnvFile(t *testing.T) {
	config, err := parseServerConfig([]string{"-f", "custom.env"})
	if err != nil {
		t.Fatalf("parseServerConfig() error = %v", err)
	}
	if config.envFile != "custom.env" {
		t.Fatalf("envFile=%q, want custom.env", config.envFile)
	}
	if _, err := parseServerConfig([]string{"-f", "custom.env", "extra"}); err == nil {
		t.Fatal("parseServerConfig() succeeded with unexpected argument")
	}
}

func unsetenvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}
