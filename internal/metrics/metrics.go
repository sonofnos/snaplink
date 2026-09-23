// Package metrics exposes Prometheus counters/histograms at /metrics so
// throughput, latency and cache hit rate can be observed in production,
// not just inferred from the local load test.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RedirectsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "snaplink_redirects_total",
		Help: "Total redirect requests by outcome (hit, not_found).",
	}, []string{"outcome"})

	CacheLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "snaplink_cache_lookups_total",
		Help: "Cache lookups on the redirect path by layer and result.",
	}, []string{"layer", "result"})

	RedirectDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "snaplink_redirect_duration_seconds",
		Help:    "Redirect handler latency.",
		Buckets: prometheus.ExponentialBuckets(0.00005, 2, 16), // 50µs .. ~1.6s
	})

	LinksCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "snaplink_links_created_total",
		Help: "Total short links created.",
	})

	RateLimitedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "snaplink_rate_limited_total",
		Help: "Create-link requests rejected by the rate limiter.",
	})

	AnalyticsQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "snaplink_analytics_queue_depth",
		Help: "Current depth of the buffered click-event channel.",
	})
)
