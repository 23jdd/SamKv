package store

import "testing"

func putStore(t *testing.T, st *StoreManger, key, value string) {
	t.Helper()
	if err := st.WriteBatch(NewBatch().Put(key, value)); err != nil {
		t.Fatal(err)
	}
}

func getStore(t *testing.T, st *StoreManger, key string) (string, bool) {
	t.Helper()
	st.mu.RLock()
	defer st.mu.RUnlock()

	if entry, ok := st.mem.table.Get(key); ok {
		if entry.Deleted {
			return "", false
		}
		return entry.Value, true
	}
	for i := len(st.immutables) - 1; i >= 0; i-- {
		immutable := st.immutables[i]
		if entry, ok := immutable.table.Get(key); ok {
			if entry.Deleted {
				return "", false
			}
			return entry.Value, true
		}
	}
	for i := len(st.sstables) - 1; i >= 0; i-- {
		record, ok, err := st.sstables[i].GetRecord(key)
		if err != nil {
			t.Helper()
			t.Fatalf("SSTable GetRecord error: %v", err)
		}
		if !ok {
			continue
		}
		if record.Deleted {
			return "", false
		}
		return record.Val, true
	}
	return "", false
}
