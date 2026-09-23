# Snaplink

A URL shortener engineered around one constraint: redirects have to stay
fast and cheap no matter how much traffic they get, because redirect
volume is what "100M+ requests/day" actually means for this class of
system (~1,160 req/s average, several times that at peak). Everything
else — link creation, stats, custom aliases — is a rounding error by
comparison and is designed for correctness, not raw speed.

Live demo: https://snaplink.sonofnos.com
Source: https://github.com/sonofnos/snaplink

## Why this isn't just `SELECT long_url FROM links WHERE code = $1`

A single Postgres instance can do maybe a few thousand simple indexed
reads per second before connection/lock contention starts hurting tail
latency, and every redirect would round-trip to a database sitting on
another host. That's the wrong shape for a read path that's 99%+ of all
traffic. So the redirect path is a cache hierarchy, and Postgres is the
system of record that traffic rarely touches directly:

```
GET /{code}
   │
   ▼
in-process LRU (per instance, ~µs, TTL 30s)  ──hit──▶ 302
   │ miss
   ▼
Redis (shared, ~sub-ms, TTL 5m)              ──hit──▶ 302 (+ warm local cache)
   │ miss
   ▼
Postgres (source of truth)                   ──found─▶ 302 (+ warm both caches)
                                              ──not found─▶ 404
```

Because link targets never change after creation, there's no cache
invalidation problem — only warming. A skewed access pattern (a handful
of links account for most clicks, which is realistic for a shortener)
means the in-process LRU alone absorbs most traffic, so most redirects
never even reach Redis.

### Writes don't block reads

Two things happen on every redirect that could, naively, slow it down:
issuing a new short code, and recording the click. Both are designed to
never touch the hot path's latency:

- **ID generation** (`internal/idgen`): a naive `INCR` in Redis per link
  created is fast, but it's still a network round trip per write, and it
  makes link *creation* depend on Redis being reachable at that instant.
  Instead each app instance reserves a **block** of 1,000 IDs at once
  (one atomic `INCRBY`), then hands out IDs from that block locally.
  ~1000x fewer coordination round trips, and instances keep creating
  links through a brief Redis blip. The trade: a restarted instance
  abandons its unused block, leaving small gaps in the ID space — fine,
  since codes aren't meant to be dense or sequential-looking to users.

- **Click analytics** (`internal/analytics`): a redirect handler that
  synchronously `INSERT`ed a row per click would cap total throughput at
  Postgres's write latency. Instead the handler does a non-blocking
  channel send; one background goroutine batches events (by size or a
  250ms timer, whichever comes first) and flushes them in a single
  `pgx.Batch`. If Postgres falls behind and the buffer fills, new events
  are dropped rather than applying backpressure to redirects — click
  analytics are best-effort, redirects are not. A live Redis counter is
  kept roughly in sync so `/api/v1/links/{code}` stats aren't stale by a
  full flush interval.

### What's rate-limited, and what isn't

Only `POST /api/v1/links` is rate-limited (distributed, via Redis, per
IP). The redirect path is deliberately never rate-limited — throughput
there is the entire point of the service, and it's a read path with no
abuse surface comparable to link creation.

## Real numbers, not a marketing claim

"Handles 100M requests/day" is a specific, checkable claim, so it's
checked: [`loadtest/redirect.js`](loadtest/redirect.js) is a k6 script
that ramps to 200 concurrent virtual users hitting the redirect endpoint
of a single container, on a laptop, over Docker's network (i.e. with
overhead a real deployment wouldn't have).

```
$ CODE=<code> k6 run loadtest/redirect.js
...
http_req_duration..: avg=5.41ms p(90)=9.55ms p(95)=12.12ms p(99)=22.78ms
http_req_failed....: 0.00%   0 out of 1,641,659
http_reqs..........: 1,641,659   23,457.9/s
```

**~23,500 redirects/sec, zero failures, p99 = 22.8ms, from one instance
on a laptop.** That's ~2 billion requests/day from a single container —
about 20x the 100M/day bar — before counting that the service is
stateless and built to scale horizontally: any number of instances can
run behind a load balancer, sharing the same Redis cache and Postgres
counter, with no coordination between instances beyond that. Real
production headroom would come from running 2-3 small instances behind
a load balancer for redundancy, not because one instance can't keep up.

Reproduce it yourself: `make up`, create a link, then
`CODE=<code> make loadtest`.

## What's actually in the box

- **Go** (net/http + [chi](https://github.com/go-chi/chi)) — no
  framework overhead on the hot path.
- **Postgres** — durable `links` + `click_events` tables, schema in
  [`internal/store/migrations`](internal/store/migrations).
- **Redis** — hot-path cache, atomic ID block reservation, distributed
  rate limiting.
- **In-process LRU** ([hashicorp/golang-lru](https://github.com/hashicorp/golang-lru)) —
  first line of defense before Redis is ever hit.
- **Prometheus metrics** at `/metrics` — redirect latency histogram,
  cache hit/miss by layer, rate-limit rejections, analytics queue depth.
- **`/healthz`** (liveness) and **`/readyz`** (checks Postgres + Redis)
  for orchestrator health checks.
- Open-redirect guard: only `http`/`https` schemes with a real host are
  accepted at creation time (no `javascript:`, no bare paths).

## API

```
POST /api/v1/links
  { "url": "https://example.com/very/long/path", "custom_code": "optional", "expires_in_seconds": 3600 }
  → 201 { "short_url", "code", "long_url", "expires_at" }

GET /{code}          → 302 redirect (or 404)
GET /api/v1/links/{code} → { "code", "long_url", "created_at", "expires_at", "clicks" }
GET /healthz, /readyz, /metrics
```

## Running it

```
make up          # postgres + redis + app via docker-compose
make test        # unit tests (base62 codec, ID allocator)
make test-integration  # full create→redirect→stats flow against real
                        # Postgres + Redis via Testcontainers
```

## Deployment

Deployed as a single Docker web service on Render's free tier
(`Dockerfile`, no custom infra needed), backed by Render Postgres and a
Redis-compatible Key Value store, both free tier. See the note in
`render.yaml` for the exact resources. Free-tier caveats that apply to
this deployment: the free Postgres instance expires 30 days after
creation and needs manual renewal, and the free web service sleeps after
inactivity (first request after idle can take up to ~a minute) — the
load test numbers above were taken against a warm local instance, not
the cold free-tier deployment, which is exactly why they're a fairer
measure of the architecture than of the free hosting tier.
