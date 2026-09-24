package store

import "time"

type Link struct {
	ID        uint64
	Code      string
	LongURL   string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Clicks    int64
	UserID    *int64
}

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type DayCount struct {
	Day    time.Time
	Clicks int64
}

type Count struct {
	Label string
	Count int64
}

type Analytics struct {
	Total     int64
	Uniques   int64
	Daily     []DayCount
	UserAgent []Count // raw user agents; classified by the handler
	Referrers []Count // raw referrer URLs; reduced to hosts by the handler
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
