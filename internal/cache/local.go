// Package cache wraps an in-process, size-bounded, TTL-expiring cache
// that sits in front of Redis on the redirect hot path.
//
// Under a skewed access pattern (a handful of links get most of the
// clicks — realistic for a URL shortener) this avoids a network round
// trip to Redis entirely for the majority of requests, which is what
// makes a single instance able to serve far more than Redis's own
// throughput ceiling would otherwise imply.
package cache

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

type entry struct {
	url       string
	expiresAt time.Time // zero = never expires
}

type Local struct {
	c *lru.LRU[string, entry]
}

func New(size int, ttl time.Duration) *Local {
	return &Local{c: lru.NewLRU[string, entry](size, nil, ttl)}
}

// Get treats a link past its own expiry as a miss even if the cache
// entry's TTL hasn't elapsed yet.
func (l *Local) Get(code string) (string, bool) {
	e, ok := l.c.Get(code)
	if !ok || (!e.expiresAt.IsZero() && !e.expiresAt.After(time.Now())) {
		return "", false
	}
	return e.url, true
}

func (l *Local) Set(code, longURL string, expiresAt *time.Time) {
	e := entry{url: longURL}
	if expiresAt != nil {
		e.expiresAt = *expiresAt
	}
	l.c.Add(code, e)
}

func (l *Local) Delete(code string) {
	l.c.Remove(code)
}
