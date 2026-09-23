// Package idgen hands out globally unique, monotonically increasing IDs
// without a per-request round trip to shared state.
//
// A naive design calls Redis INCR once per shortened URL created. That's
// fine well past 100M redirects/day (writes are a small fraction of
// traffic) but it's still one network round trip per write, and it makes
// every app instance depend on Redis being reachable to create a link.
//
// Instead each instance reserves a *block* of IDs at once (BlockSize,
// default 1000) via a single atomic INCRBY, then hands out IDs from that
// block locally until it runs out. This cuts coordination traffic by
// ~1000x and lets link creation keep working, briefly, through a Redis
// blip — at the cost of leaving gaps in the ID space when an instance
// restarts with unused IDs in its block. That's an acceptable trade for
// a short-code allocator (codes aren't sequential-facing to users, and
// base62 doesn't need dense packing to stay short).
package idgen

import (
	"context"
	"sync"
)

// Reserver atomically reserves a contiguous block of IDs starting at the
// returned value, i.e. it must return start such that
// [start, start+size) is owned exclusively by this call.
type Reserver interface {
	ReserveBlock(ctx context.Context, size uint64) (start uint64, err error)
}

const DefaultBlockSize = 1000

type Allocator struct {
	mu        sync.Mutex
	reserver  Reserver
	blockSize uint64
	next      uint64
	end       uint64 // exclusive upper bound of the current block
}

func New(reserver Reserver, blockSize uint64) *Allocator {
	if blockSize == 0 {
		blockSize = DefaultBlockSize
	}
	return &Allocator{reserver: reserver, blockSize: blockSize}
}

// Next returns the next unique ID, transparently reserving a new block
// from the Reserver when the local one is exhausted.
func (a *Allocator) Next(ctx context.Context) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.next >= a.end {
		start, err := a.reserver.ReserveBlock(ctx, a.blockSize)
		if err != nil {
			return 0, err
		}
		a.next = start
		a.end = start + a.blockSize
	}

	id := a.next
	a.next++
	return id, nil
}
