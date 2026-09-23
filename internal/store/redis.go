package store

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	urlKeyPrefix     = "url:"     // code -> long URL, hot-path cache
	counterKey       = "id:seq"   // shared monotonic counter, INCRBY'd in blocks
	rateLimitPrefix  = "rl:"      // per-IP sliding window counter for the create endpoint
	clicksKeyPrefix  = "clicks:"  // code -> live click counter (fast reads, eventually reconciled with Postgres)
)

type Redis struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedis(opts *redis.Options, cacheTTL time.Duration) *Redis {
	return &Redis{client: redis.NewClient(opts), ttl: cacheTTL}
}

func ParseRedisURL(rawURL string) (*redis.Options, error) {
	return redis.ParseURL(rawURL)
}

func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *Redis) Close() error {
	return r.client.Close()
}

// --- hot-path URL cache ---

func (r *Redis) GetURL(ctx context.Context, code string) (string, error) {
	v, err := r.client.Get(ctx, urlKeyPrefix+code).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	return v, err
}

func (r *Redis) SetURL(ctx context.Context, code, longURL string) error {
	return r.client.Set(ctx, urlKeyPrefix+code, longURL, r.ttl).Err()
}

// EnsureCounterFloor seeds the ID counter so freshly-created codes don't
// start at "0"/"1"/etc (technically fine, just looks unfinished for a
// portfolio demo). SETNX is a no-op once the counter already exists.
func (r *Redis) EnsureCounterFloor(ctx context.Context, floor uint64) error {
	return r.client.SetNX(ctx, counterKey, floor, 0).Err()
}

// --- idgen.Reserver ---

// ReserveBlock atomically reserves [start, start+size) via a single INCRBY.
func (r *Redis) ReserveBlock(ctx context.Context, size uint64) (uint64, error) {
	newTotal, err := r.client.IncrBy(ctx, counterKey, int64(size)).Result()
	if err != nil {
		return 0, err
	}
	return uint64(newTotal) - size, nil
}

// --- live click counters ---

func (r *Redis) IncrClicks(ctx context.Context, code string, n int64) error {
	return r.client.IncrBy(ctx, clicksKeyPrefix+code, n).Err()
}

func (r *Redis) GetLiveClicks(ctx context.Context, code string) (int64, error) {
	v, err := r.client.Get(ctx, clicksKeyPrefix+code).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

// --- distributed rate limiting (fixed window, per IP) ---
// Only guards the write path (link creation); the redirect hot path is
// intentionally never rate limited here since throughput on that path is
// the entire point of the service.

func (r *Redis) AllowCreate(ctx context.Context, ip string, limit int, window time.Duration) (bool, error) {
	key := rateLimitPrefix + ip
	count, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		r.client.Expire(ctx, key, window)
	}
	return count <= int64(limit), nil
}
