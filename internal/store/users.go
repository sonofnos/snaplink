package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (p *Postgres) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	u := User{Email: email, PasswordHash: passwordHash}
	err := p.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, created_at`,
		email, passwordHash).Scan(&u.ID, &u.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrConflict
	}
	return u, err
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := p.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (p *Postgres) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expires time.Time) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expires)
	return err
}

func (p *Postgres) GetSessionUser(ctx context.Context, tokenHash []byte) (User, error) {
	var u User
	err := p.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.password_hash, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (p *Postgres) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (p *Postgres) ListLinksByUser(ctx context.Context, userID int64, limit int) ([]Link, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, code, long_url, created_at, expires_at, clicks, user_id
		FROM links WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.Code, &l.LongURL, &l.CreatedAt, &l.ExpiresAt, &l.Clicks, &l.UserID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLink removes a link and its click history, only if userID owns it.
func (p *Postgres) DeleteLink(ctx context.Context, userID int64, code string) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM links WHERE code = $1 AND user_id = $2`, code, userID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM click_events WHERE code = $1`, code); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// LinkAnalytics aggregates click_events for one link over the last `days` days.
func (p *Postgres) LinkAnalytics(ctx context.Context, code string, days int) (Analytics, error) {
	var a Analytics
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	if err := p.pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT ip) FROM click_events WHERE code = $1 AND ts >= $2`,
		code, since).Scan(&a.Total, &a.Uniques); err != nil {
		return a, err
	}

	rows, err := p.pool.Query(ctx, `
		SELECT date_trunc('day', ts AT TIME ZONE 'UTC') AS d, count(*)
		FROM click_events WHERE code = $1 AND ts >= $2 GROUP BY d ORDER BY d`, code, since)
	if err != nil {
		return a, err
	}
	byDay := map[string]int64{}
	for rows.Next() {
		var d time.Time
		var n int64
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return a, err
		}
		byDay[d.Format("2006-01-02")] = n
	}
	rows.Close()
	for i := 0; i < days; i++ {
		d := since.AddDate(0, 0, i)
		a.Daily = append(a.Daily, DayCount{Day: d, Clicks: byDay[d.Format("2006-01-02")]})
	}

	if a.UserAgent, err = p.topCounts(ctx, `
		SELECT coalesce(user_agent,''), count(*) FROM click_events
		WHERE code = $1 AND ts >= $2 GROUP BY 1 ORDER BY 2 DESC LIMIT 200`, code, since); err != nil {
		return a, err
	}
	a.Referrers, err = p.topCounts(ctx, `
		SELECT coalesce(referrer,''), count(*) FROM click_events
		WHERE code = $1 AND ts >= $2 GROUP BY 1 ORDER BY 2 DESC LIMIT 200`, code, since)
	return a, err
}

func (p *Postgres) topCounts(ctx context.Context, q, code string, since time.Time) ([]Count, error) {
	rows, err := p.pool.Query(ctx, q, code, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Label, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PurgeOldClickEvents keeps click_events bounded (the free Postgres is 1GB).
func (p *Postgres) PurgeOldClickEvents(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM click_events WHERE ts < $1`, time.Now().Add(-olderThan))
	return tag.RowsAffected(), err
}

func (p *Postgres) PurgeExpiredSessions(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}
