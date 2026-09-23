package store

import "time"

type Link struct {
	ID        uint64
	Code      string
	LongURL   string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Clicks    int64
}

// ClickEvent is a single redirect hit, queued in memory and batch-written
// to Postgres so the redirect hot path never waits on a database write.
type ClickEvent struct {
	Code      string
	Timestamp time.Time
	IP        string
	UserAgent string
	Referrer  string
}
