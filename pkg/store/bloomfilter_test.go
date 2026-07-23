package store

import "testing"

func TestBloomFilterRoundTripAndReset(t *testing.T) {
	filter, err := NewBloomFilter(100, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"alpha", "beta", "gamma"} {
		filter.AddString(key)
	}
	for _, key := range []string{"alpha", "beta", "gamma"} {
		if !filter.ContainsString(key) {
			t.Fatalf("filter rejected written key %q", key)
		}
	}

	data, err := filter.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var restored BloomFilter
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if restored.BitSize() != filter.BitSize() ||
		restored.HashCount() != filter.HashCount() ||
		restored.Count() != filter.Count() {
		t.Fatal("restored BloomFilter metadata differs")
	}
	if !restored.ContainsString("beta") {
		t.Fatal("restored BloomFilter rejected beta")
	}

	restored.Reset()
	if restored.Count() != 0 || restored.ContainsString("alpha") {
		t.Fatal("Reset() did not clear BloomFilter")
	}
}

func TestBloomFilterRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewBloomFilter(0, 0.01); err == nil {
		t.Fatal("expected zero item count error")
	}
	if _, err := NewBloomFilter(1, 1); err == nil {
		t.Fatal("expected invalid false positive rate error")
	}
	if _, err := NewBloomFilterWithSize(0, 1); err == nil {
		t.Fatal("expected zero bit size error")
	}
	if _, err := NewBloomFilterWithSize(64, 0); err == nil {
		t.Fatal("expected zero hash count error")
	}
}
