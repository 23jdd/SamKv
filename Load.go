package main

import (
	"os"
	"strconv"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/joho/godotenv"
)

// Load 从 .env 和进程环境读取服务配置；无效或缺失值保留库默认值。
func Load() store.Options {
	_ = godotenv.Load(".env")
	options := store.DefaultOptions()

	if value, err := strconv.Atoi(os.Getenv("MemTableLimit")); err == nil {
		options.MemTableLimit = value
	}
	if value, err := strconv.ParseBool(os.Getenv("AutoCheckpoint")); err == nil {
		options.AutoCheckpoint = value
	}
	if value, err := strconv.Atoi(os.Getenv("CompactionThreshold")); err == nil {
		options.CompactionThreshold = value
	}
	if value, err := strconv.Atoi(os.Getenv("Retention")); err == nil {
		options.Retention = time.Duration(value) * time.Hour
	}
	if value, err := strconv.ParseInt(os.Getenv("MaxSizeBytes"), 10, 64); err == nil {
		options.MaxSizeBytes = value
	}
	if value, err := strconv.ParseInt(os.Getenv("BlockCacheBytes"), 10, 64); err == nil {
		options.BlockCacheBytes = value
	}
	if value, err := strconv.Atoi(os.Getenv("MaxLevels")); err == nil {
		options.MaxLevels = value
	}
	if value, err := strconv.ParseInt(os.Getenv("LevelBaseSizeBytes"), 10, 64); err == nil {
		options.LevelBaseSizeBytes = value
	}
	if value, err := strconv.Atoi(os.Getenv("LevelSizeMultiplier")); err == nil {
		options.LevelSizeMultiplier = value
	}
	switch os.Getenv("WALSyncPolicy") {
	case "every-write":
		options.WALSyncPolicy = store.WALSyncEveryWrite
	case "interval":
		options.WALSyncPolicy = store.WALSyncInterval
	}
	if value, err := time.ParseDuration(os.Getenv("WALSyncInterval")); err == nil {
		options.WALSyncInterval = value
	}
	return options
}
