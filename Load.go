package main

import (
	"os"
	"strconv"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/joho/godotenv"
)

func Load()store.Options{
	err := godotenv.Load(".env")
	options := store.DefaultOptions()
	if err == nil {
		memlimit, err := strconv.Atoi(os.Getenv("MemTableLimit"))
		if err == nil {
			options.MemTableLimit = memlimit
		}
		autocheck, err := strconv.ParseBool(os.Getenv("AutoCheckpoint"))
		if err == nil {
			options.AutoCheckpoint = autocheck
		}
		threshold, err := strconv.Atoi(os.Getenv("CompactionThreshold"))
		if err == nil {
			options.CompactionThreshold = threshold
		}
		hours, err := strconv.Atoi(os.Getenv("Retention"))
		if err == nil {
			options.Retention = time.Duration(hours) * time.Hour
		}
		maxsize, err := strconv.ParseInt(os.Getenv("MaxSizeBytes"), 10, 64)
		if err == nil {
			options.MaxSizeBytes = maxsize
		}
	}
	return options
}
