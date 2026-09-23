//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sonofnos/snaplink/internal/analytics"
	"github.com/sonofnos/snaplink/internal/cache"
	"github.com/sonofnos/snaplink/internal/handlers"
	"github.com/sonofnos/snaplink/internal/idgen"
	"github.com/sonofnos/snaplink/internal/store"
)

// setup spins up real Postgres + Redis containers and wires the full
// handler stack, exactly as main.go does, so this test exercises the
// same code paths production traffic hits.
func setup(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("snaplink"),
		tcpostgres.WithUsername("snaplink"),
		tcpostgres.WithPassword("snaplink"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(ctx) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}

	redisC, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	t.Cleanup(func() { _ = redisC.Terminate(ctx) })
	redisURI, err := redisC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis uri: %v", err)
	}

	db, err := store.NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, store.Schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	opts, err := store.ParseRedisURL(redisURI)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := store.NewRedis(opts, 5*time.Minute)
	t.Cleanup(func() { _ = rdb.Close() })

	logger := slog.New(slog.NewTextHandler(testingWriter{t}, nil))
	local := cache.New(1000, 30*time.Second)
	ids := idgen.New(db, 1000)
	an := analytics.New(db, rdb, 1000, 50*time.Millisecond, logger)

	analyticsCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go an.Run(analyticsCtx)

	h := handlers.New(db, rdb, local, ids, an, "http://localhost:8080", 1000, logger)
	return h.Routes(nil)
}

type testingWriter struct{ t *testing.T }

func (w testingWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func TestCreateRedirectStatsFlow(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	// Create
	createBody := strings.NewReader(`{"url":"https://example.com/some/long/path?x=1"}`)
	resp, err := client.Post(srv.URL+"/api/v1/links", "application/json", createBody)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var created struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	resp.Body.Close()
	if created.Code == "" {
		t.Fatal("expected non-empty code")
	}

	// Redirect
	redirectResp, err := client.Get(srv.URL + "/" + created.Code)
	if err != nil {
		t.Fatalf("redirect request failed: %v", err)
	}
	defer redirectResp.Body.Close()
	if redirectResp.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", redirectResp.StatusCode)
	}
	if loc := redirectResp.Header.Get("Location"); loc != "https://example.com/some/long/path?x=1" {
		t.Fatalf("redirect location = %q", loc)
	}

	// Second redirect should be served from local/Redis cache, not Postgres.
	redirectResp2, err := client.Get(srv.URL + "/" + created.Code)
	if err != nil {
		t.Fatalf("second redirect request failed: %v", err)
	}
	redirectResp2.Body.Close()
	if redirectResp2.StatusCode != http.StatusFound {
		t.Fatalf("second redirect status = %d, want 302", redirectResp2.StatusCode)
	}

	// Stats eventually reflect both clicks once the async writer flushes.
	deadline := time.Now().Add(3 * time.Second)
	var clicks int64
	for time.Now().Before(deadline) {
		statsResp, err := client.Get(srv.URL + "/api/v1/links/" + created.Code)
		if err != nil {
			t.Fatalf("stats request failed: %v", err)
		}
		var stats struct {
			Clicks int64 `json:"clicks"`
		}
		_ = json.NewDecoder(statsResp.Body).Decode(&stats)
		statsResp.Body.Close()
		clicks = stats.Clicks
		if clicks >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if clicks < 2 {
		t.Fatalf("expected clicks >= 2 after async flush, got %d", clicks)
	}
}

func TestRedirectUnknownCodeReturns404(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/doesnotexist")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateRejectsNonHTTPScheme(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := strings.NewReader(`{"url":"javascript:alert(1)"}`)
	resp, err := srv.Client().Post(srv.URL+"/api/v1/links", "application/json", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateWithCustomCodeConflict(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	first := strings.NewReader(`{"url":"https://example.com/a","custom_code":"mycode"}`)
	resp1, err := srv.Client().Post(srv.URL+"/api/v1/links", "application/json", first)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", resp1.StatusCode)
	}

	second := strings.NewReader(`{"url":"https://example.com/b","custom_code":"mycode"}`)
	resp2, err := srv.Client().Post(srv.URL+"/api/v1/links", "application/json", second)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", resp2.StatusCode)
	}
}

func TestExpiredLinkReturns404(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Post(srv.URL+"/api/v1/links", "application/json",
		strings.NewReader(`{"url":"https://example.com/e","expires_in_seconds":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	live, _ := client.Get(srv.URL + "/" + created.Code)
	live.Body.Close()
	if live.StatusCode != http.StatusFound {
		t.Fatalf("before expiry status = %d, want 302", live.StatusCode)
	}

	time.Sleep(2 * time.Second)
	gone, _ := client.Get(srv.URL + "/" + created.Code)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("after expiry status = %d, want 404", gone.StatusCode)
	}
}
