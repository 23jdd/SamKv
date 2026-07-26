package store

import "testing"

func TestDefaultOptionsConfiguresCompactionWorkers(t *testing.T) {
	options := DefaultOptions()
	if options.CompactionWorkers != DefaultCompactionWorkers {
		t.Fatalf("CompactionWorkers = %d, want %d", options.CompactionWorkers, DefaultCompactionWorkers)
	}
}

func TestValidateOptionsRejectsInvalidCompactionWorkers(t *testing.T) {
	options := DefaultOptions()
	options.CompactionWorkers = 0
	if err := validateOptions(options); err != ErrInvalidOptions {
		t.Fatalf("validateOptions() error = %v, want %v", err, ErrInvalidOptions)
	}
}
