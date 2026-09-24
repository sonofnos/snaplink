//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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
	if err := db.Migrate(ctx); err != nil {
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

	h := handlers.New(db, rdb, local, ids, an, "http://localhost:8080", 1000, 2, logger)
	return h.Routes(nil)
}

// signedInClient returns a client (with cookie jar) signed up as a new user.
func signedInClient(t *testing.T, srvURL, email string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Post(srvURL+"/api/v1/auth/signup", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"correct horse battery"}`, email)))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup status = %d, want 201", resp.StatusCode)
	}
	return c
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
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
	c := signedInClient(t, srv.URL, "alias@example.com")

	first := strings.NewReader(`{"url":"https://example.com/a","custom_code":"mycode"}`)
	resp1, err := c.Post(srv.URL+"/api/v1/links", "application/json", first)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", resp1.StatusCode)
	}

	second := strings.NewReader(`{"url":"https://example.com/b","custom_code":"mycode"}`)
	resp2, err := c.Post(srv.URL+"/api/v1/links", "application/json", second)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", resp2.StatusCode)
	}

	reserved, _ := c.Post(srv.URL+"/api/v1/links", "application/json", strings.NewReader(`{"url":"https://example.com/c","custom_code":"static"}`))
	reserved.Body.Close()
	if reserved.StatusCode != http.StatusConflict {
		t.Fatalf("reserved code status = %d, want 409", reserved.StatusCode)
	}
}

func TestAnonymousCannotUseAccountFeatures(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	for _, body := range []string{
		`{"url":"https://example.com/a","custom_code":"anonalias"}`,
		`{"url":"https://example.com/a","expires_in_seconds":60}`,
	} {
		resp, err := srv.Client().Post(srv.URL+"/api/v1/links", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous %s -> %d, want 401", body, resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/v1/me/links"} {
		resp, _ := srv.Client().Get(srv.URL + path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s -> %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestAuthRules(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()
	signedInClient(t, srv.URL, "dupe@example.com")

	post := func(path, ct, body string) int {
		resp, err := srv.Client().Post(srv.URL+path, ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("/api/v1/auth/signup", "application/json", `{"email":"dupe@example.com","password":"correct horse battery"}`); got != http.StatusConflict {
		t.Errorf("duplicate signup = %d, want 409", got)
	}
	if got := post("/api/v1/auth/signup", "application/json", `{"email":"short@example.com","password":"short"}`); got != http.StatusBadRequest {
		t.Errorf("short password = %d, want 400", got)
	}
	if got := post("/api/v1/auth/signup", "application/json", `{"email":"not-an-email","password":"correct horse battery"}`); got != http.StatusBadRequest {
		t.Errorf("bad email = %d, want 400", got)
	}
	if got := post("/api/v1/auth/login", "application/json", `{"email":"dupe@example.com","password":"wrong password here"}`); got != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", got)
	}
	if got := post("/api/v1/auth/login", "application/json", `{"email":"nobody@example.com","password":"wrong password here"}`); got != http.StatusUnauthorized {
		t.Errorf("unknown user = %d, want 401", got)
	}
	if got := post("/api/v1/auth/login", "application/x-www-form-urlencoded", `email=dupe@example.com&password=correct+horse+battery`); got != http.StatusUnsupportedMediaType {
		t.Errorf("form-encoded login (CSRF guard) = %d, want 415", got)
	}
}

func TestDashboardFlow(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()
	alice := signedInClient(t, srv.URL, "alice@example.com")
	bob := signedInClient(t, srv.URL, "bob@example.com")

	resp, err := alice.Post(srv.URL+"/api/v1/links", "application/json", strings.NewReader(`{"url":"https://example.com/alice","custom_code":"alice-link"}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %v %v", err, resp)
	}
	resp.Body.Close()

	// Three clicks (with a referrer), then wait for the async flush.
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", srv.URL+"/alice-link", nil)
		req.Header.Set("Referer", "https://www.news.example/story")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
		r, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}

	type analytics struct {
		Total    int64                    `json:"total"`
		Uniques  int64                    `json:"uniques"`
		Daily    []struct{ Clicks int64 } `json:"daily"`
		Browsers []struct {
			Label string
			Count int64
		} `json:"browsers"`
		Referrers []struct {
			Label string
			Count int64
		} `json:"referrers"`
	}
	var a analytics
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := alice.Get(srv.URL + "/api/v1/me/links/alice-link/analytics")
		a = analytics{}
		decode(t, r, &a)
		if a.Total >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if a.Total != 3 || len(a.Daily) != 30 {
		t.Fatalf("analytics total=%d days=%d, want 3 and 30", a.Total, len(a.Daily))
	}
	if len(a.Browsers) == 0 || a.Browsers[0].Label != "Chrome" {
		t.Errorf("browsers = %+v, want Chrome first", a.Browsers)
	}
	if len(a.Referrers) == 0 || a.Referrers[0].Label != "news.example" {
		t.Errorf("referrers = %+v, want news.example (www stripped)", a.Referrers)
	}

	var mine []struct{ Code string }
	r, _ := alice.Get(srv.URL + "/api/v1/me/links")
	decode(t, r, &mine)
	if len(mine) != 1 || mine[0].Code != "alice-link" {
		t.Fatalf("alice's links = %+v", mine)
	}
	r, _ = bob.Get(srv.URL + "/api/v1/me/links")
	var bobs []struct{ Code string }
	decode(t, r, &bobs)
	if len(bobs) != 0 {
		t.Errorf("bob sees %d links, want 0", len(bobs))
	}

	// Ownership: bob can neither read analytics for, nor delete, alice's link.
	r, _ = bob.Get(srv.URL + "/api/v1/me/links/alice-link/analytics")
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("bob analytics = %d, want 404", r.StatusCode)
	}
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/v1/me/links/alice-link", nil)
	req.Header.Set("Content-Type", "application/json")
	r, _ = bob.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("bob delete = %d, want 404", r.StatusCode)
	}

	// Owner delete removes the link everywhere, caches included.
	req, _ = http.NewRequest("DELETE", srv.URL+"/api/v1/me/links/alice-link", nil)
	req.Header.Set("Content-Type", "application/json")
	r, _ = alice.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("alice delete = %d, want 204", r.StatusCode)
	}
	r, _ = srv.Client().Get(srv.URL + "/alice-link")
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("deleted link redirect = %d, want 404", r.StatusCode)
	}

	// Logout invalidates the session server-side.
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	r, _ = alice.Do(req)
	r.Body.Close()
	r, _ = alice.Get(srv.URL + "/api/v1/auth/me")
	var me struct{ Email string }
	decode(t, r, &me)
	if me.Email != "" {
		t.Errorf("after logout /auth/me email = %q, want empty", me.Email)
	}
	r, _ = alice.Get(srv.URL + "/api/v1/me/links")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout /me/links = %d, want 401", r.StatusCode)
	}
}

func TestExpiredLinkReturns404(t *testing.T) {
	router := setup(t)
	srv := httptest.NewServer(router)
	defer srv.Close()
	client := signedInClient(t, srv.URL, "expiry@example.com")

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
