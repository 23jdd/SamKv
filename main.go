package main


import (
	"fmt"
	"log"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

func main() {
	options := store.DefaultOptions()
	options.MemTableLimit = 4 * 1024 * 1024
	options.CompactionThreshold = 4
	options.Retention = 7 * 24 * time.Hour
	options.MaxSizeBytes = 10 * 1024 * 1024 * 1024

	db, err := store.NewStoreManagerWithOptions("./data", options)
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

