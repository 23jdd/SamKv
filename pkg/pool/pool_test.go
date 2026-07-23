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

func TestTieredPoolRejectsInvalidCapacities(t *testing.T) {
	tests := []struct {
		name       string
		capacities []int
	}{
		{name: "empty"},
		{name: "zero", capacities: []int{0}},
		{name: "negative", capacities: []int{-1}},
		{name: "duplicate", capacities: []int{8, 8}},
		{name: "descending", capacities: []int{16, 8}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewTieredPool() did not panic")
				}
			}()
			NewTieredPool(test.capacities...)
		})
	}
}

func TestTieredPoolCopiesCapacities(t *testing.T) {
	capacities := []int{8, 16}
	pool := NewTieredPool(capacities...)
	capacities[0] = 1

	buf := pool.Get(8)
	if cap(buf) != 8 {
		t.Fatalf("Get(8) cap=%d, want 8", cap(buf))
	}
}

func TestTieredPoolDiscardsForeignCapacity(t *testing.T) {
	pool := NewTieredPool(8, 16)
	pool.Put(make([]byte, 0, 12))

	buf := pool.pools[1].Get().([]byte)
	if cap(buf) != 16 {
		t.Fatalf("bucket capacity=%d, want 16", cap(buf))
	}
}

func TestTieredPoolRejectsNegativeSize(t *testing.T) {
	pool := NewTieredPool(8)
	defer func() {
		if recover() == nil {
			t.Fatal("Get(-1) did not panic")
		}
	}()
	pool.Get(-1)
}
