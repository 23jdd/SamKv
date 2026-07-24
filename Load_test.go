package main

import (
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
