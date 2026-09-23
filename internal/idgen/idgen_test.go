package idgen

import (
	"context"
	"sync"
	"testing"
)

// fakeReserver mimics a Redis INCRBY counter in memory.
type fakeReserver struct {
	mu      sync.Mutex
	counter uint64
	calls   int
}

func (f *fakeReserver) ReserveBlock(ctx context.Context, size uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	start := f.counter
	f.counter += size
	f.calls++
	return start, nil
}

func TestAllocatorProducesUniqueSequentialIDs(t *testing.T) {
	r := &fakeReserver{}
	a := New(r, 10)

	seen := make(map[uint64]bool)
	for i := 0; i < 105; i++ {
		id, err := a.Next(context.Background())
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
	// 105 IDs from blocks of 10 must reserve exactly 11 blocks.
	if r.calls != 11 {
		t.Errorf("expected 11 ReserveBlock calls, got %d", r.calls)
	}
}

func TestAllocatorConcurrentSafety(t *testing.T) {
	r := &fakeReserver{}
	a := New(r, 50)

	const goroutines = 20
	const perGoroutine = 200
	ids := make(chan uint64, goroutines*perGoroutine)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				id, err := a.Next(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[uint64]bool, goroutines*perGoroutine)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %d under concurrency", id)
		}
		seen[id] = true
	}
	if len(seen) != goroutines*perGoroutine {
		t.Errorf("got %d unique ids, want %d", len(seen), goroutines*perGoroutine)
	}
}
