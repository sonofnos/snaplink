// Package analytics decouples click tracking from the redirect hot path.
//
// A redirect handler that synchronously wrote a row to Postgres per click
// would cap the whole service's throughput at Postgres's write latency.
// Instead handlers do a non-blocking channel send; a single background
// goroutine drains the channel and flushes to Postgres in batches, either
// when a batch fills up or on a timer — whichever comes first. If the
// buffer is ever full (Postgres falling behind, or down), events are
// dropped rather than blocking or crashing a redirect: click analytics
// are best-effort, redirects are not.
package analytics

import (
	"context"
	"log/slog"
	"time"

	"github.com/sonofnos/snaplink/internal/store"
)

type Sink interface {
	InsertClickEvents(ctx context.Context, events []store.ClickEvent) error
}

type Writer struct {
	events chan store.ClickEvent
	sink   Sink
	live   LiveCounter
	flush  time.Duration
	logger *slog.Logger
}

// LiveCounter is the fast, eventually-consistent Redis counter used for
// stats reads; kept in sync opportunistically as events are flushed.
type LiveCounter interface {
	IncrClicks(ctx context.Context, code string, n int64) error
}

func New(sink Sink, live LiveCounter, bufferSize int, flushInterval time.Duration, logger *slog.Logger) *Writer {
	return &Writer{
		events: make(chan store.ClickEvent, bufferSize),
		sink:   sink,
		live:   live,
		flush:  flushInterval,
		logger: logger,
	}
}

// Record queues a click event. Non-blocking: drops the event and logs at
// debug level if the buffer is saturated, rather than applying backpressure
// to the caller (the redirect handler).
func (w *Writer) Record(e store.ClickEvent) {
	select {
	case w.events <- e:
	default:
		w.logger.Debug("analytics buffer full, dropping click event", "code", e.Code)
	}
}

// Run drains and batches events until ctx is cancelled. Intended to run in
// its own goroutine for the lifetime of the process.
func (w *Writer) Run(ctx context.Context) {
	ticker := time.NewTicker(w.flush)
	defer ticker.Stop()

	const maxBatch = 500
	batch := make([]store.ClickEvent, 0, maxBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.sink.InsertClickEvents(ctx, batch); err != nil {
			w.logger.Error("failed to flush click events", "error", err, "count", len(batch))
		} else {
			counts := make(map[string]int64, len(batch))
			for _, e := range batch {
				counts[e.Code]++
			}
			for code, n := range counts {
				_ = w.live.IncrClicks(ctx, code, n)
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case e := <-w.events:
			batch = append(batch, e)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
