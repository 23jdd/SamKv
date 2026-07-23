package tcp

import "testing"

func TestTieredPoolChoosesSmallestCapacity(t *testing.T) {
	pool := NewTieredPool(8, 16)

	small := pool.Get(5)
	if len(small) != 5 || cap(small) != 8 {
		t.Fatalf("Get(5) len=%d cap=%d", len(small), cap(small))
	}
	pool.Put(small)

	medium := pool.Get(9)
	if len(medium) != 9 || cap(medium) != 16 {
		t.Fatalf("Get(9) len=%d cap=%d", len(medium), cap(medium))
	}
	pool.Put(medium)

	large := pool.Get(32)
	if len(large) != 32 || cap(large) < 32 {
		t.Fatalf("Get(32) len=%d cap=%d", len(large), cap(large))
	}
	pool.Put(large)
}

func TestTieredPoolRequiresCapacities(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewTieredPool() did not panic")
		}
	}()
	NewTieredPool()
}
