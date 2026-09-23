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

type Local struct {
	c *lru.LRU[string, string]
}

func New(size int, ttl time.Duration) *Local {
	return &Local{c: lru.NewLRU[string, string](size, nil, ttl)}
}

func (l *Local) Get(code string) (string, bool) {
	return l.c.Get(code)
}

func (l *Local) Set(code, longURL string) {
	l.c.Add(code, longURL)
}
