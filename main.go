package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
	"github.com/joho/godotenv"
)

func main() {
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
	dir:= os.Getenv("dir")
	if dir==""{
		   dir="./data"
	}
	db, err := store.NewStoreManagerWithOptions(dir, options)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.WriteLog(store.LogEntry{
		Timestamp: time.Now().UTC(),
		Labels: []utils.Label{
			{Name: "app", Value: "nginx"},
			{Name: "level", Value: "ERROR"},
		},
		Message: []byte("upstream connection failed"),
	})
	if err != nil {
		log.Fatal(err)
	}

	end := time.Now().UTC()
	start := end.Add(-time.Hour)
	logs, err := db.Query(start, end, []utils.Label{
		{Name: "app", Value: "nginx"},
		{Name: "level", Value: "ERROR"},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range logs {
		fmt.Printf("%s %s", entry.Timestamp.Format(time.RFC3339Nano), entry.Message)
	}
}
