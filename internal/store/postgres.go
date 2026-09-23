package store

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/0001_init.sql
var Schema string

var ErrNotFound = errors.New("store: link not found")

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Redirects are the overwhelming majority of traffic but almost all of
	// them are served from Redis/local cache; Postgres mainly absorbs
	// writes (new links + batched click events), so a modest pool is
	// enough and keeps us inside free-tier connection limits.
	cfg.MaxConns = 10
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Migrate(ctx context.Context, schema string) error {
	_, err := p.pool.Exec(ctx, schema)
	return err
}

func (p *Postgres) InsertLink(ctx context.Context, l Link) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO links (id, code, long_url, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (code) DO NOTHING
	`, l.ID, l.Code, l.LongURL, l.CreatedAt, l.ExpiresAt)
	return err
}

func (p *Postgres) GetLinkByCode(ctx context.Context, code string) (Link, error) {
	var l Link
	err := p.pool.QueryRow(ctx, `
		SELECT id, code, long_url, created_at, expires_at, clicks
		FROM links WHERE code = $1
	`, code).Scan(&l.ID, &l.Code, &l.LongURL, &l.CreatedAt, &l.ExpiresAt, &l.Clicks)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

// InsertClickEvents batch-inserts click events and bumps each link's
// denormalized click counter in one round trip. Called by the async
// analytics writer, never from the redirect request path.
func (p *Postgres) InsertClickEvents(ctx context.Context, events []ClickEvent) error {
	if len(events) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	counts := make(map[string]int64, len(events))
	for _, e := range events {
		batch.Queue(`
			INSERT INTO click_events (code, ts, ip, user_agent, referrer)
			VALUES ($1, $2, $3, $4, $5)
		`, e.Code, e.Timestamp, e.IP, e.UserAgent, e.Referrer)
		counts[e.Code]++
	}
	for code, n := range counts {
		batch.Queue(`UPDATE links SET clicks = clicks + $1 WHERE code = $2`, n, code)
	}

	br := p.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// ReserveBlock implements idgen.Reserver against the link_id_seq sequence
// (see migrations/0001_init.sql). The sequence's INCREMENT BY fixes the
// real block size at 1000 regardless of the size argument — a single
// nextval() call reserves exactly one block, and the block's first ID is
// (nextval() - 999).
func (p *Postgres) ReserveBlock(ctx context.Context, _ uint64) (uint64, error) {
	var next int64
	if err := p.pool.QueryRow(ctx, `SELECT nextval('link_id_seq')`).Scan(&next); err != nil {
		return 0, err
	}
	return uint64(next) - 999, nil
}

func (p *Postgres) Stats(ctx context.Context, code string) (Link, error) {
	return p.GetLinkByCode(ctx, code)
}

func (p *Postgres) Healthy(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.pool.Ping(c)
}
