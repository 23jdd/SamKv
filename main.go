package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/23jdd/SamKv/pkg/store"
	"github.com/23jdd/SamKv/pkg/utils"
)

func main() {
	options := Load()
	dir := os.Getenv("dir")
	if dir == "" {
		dir = "./data"
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
