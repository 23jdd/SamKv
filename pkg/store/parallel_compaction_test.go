package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestParallelCompactionReadsReopenedTables(t *testing.T) {
	dir := t.TempDir()
	options := DefaultOptions()
	options.AutoCheckpoint = false
	options.CompactionThreshold = 0
	options.CompactionWorkers = 4
	options.CompactionTaskBytes = 1

	database, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	const (
		generations = 4
		keyCount    = 1000
	)
	for generation := 0; generation < generations; generation++ {
		for index := 0; index < keyCount; index++ {
			key := fmt.Sprintf("key-%04d", index)
			if generation == generations-1 && index%10 == 0 {
				if err := database.Delete(key); err != nil {
					t.Fatal(err)
				}
				continue
			}
			value := fmt.Sprintf("generation-%d-%s", generation, strings.Repeat("x", 64))
			if err := database.Put(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := database.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.CompactLevel(0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Subtasks != options.CompactionWorkers || result.OutputTables != options.CompactionWorkers {
		t.Fatalf("parallel compaction result = %#v", result)
	}
	if result.InputRecords != generations*keyCount || result.OutputRecords != keyCount {
		t.Fatalf("compaction record counts = %#v", result)
	}
	if countLevel(database.manifest, 0) != 0 || countLevel(database.manifest, 1) != options.CompactionWorkers {
		t.Fatalf("compacted levels = %#v", database.manifest.SSTables)
	}
	for index := 1; index < len(database.manifest.SSTables); index++ {
		if database.manifest.SSTables[index-1].MaxKey >= database.manifest.SSTables[index].MinKey {
			t.Fatalf("parallel output ranges overlap: %#v", database.manifest.SSTables)
		}
	}
	verification, err := database.Verify()
	if err != nil || verification.Tables != result.OutputTables || verification.Records != keyCount {
		t.Fatalf("Verify() = %#v, %v", verification, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStoreManagerWithOptions(dir, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for index := 0; index < keyCount; index++ {
		key := fmt.Sprintf("key-%04d", index)
		value, found := reopened.Get(key)
		if index%10 == 0 {
			if found {
				t.Fatalf("deleted key %q was resurrected with %q", key, value)
			}
			continue
		}
		if !found || !strings.HasPrefix(value, "generation-3-") {
			t.Fatalf("Get(%q) = %q, %v", key, value, found)
		}
	}
}
