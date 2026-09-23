// Package handlers wires HTTP requests to the shortening service. The
// redirect handler is the hot path this whole service is built around;
// every other handler favors clarity over raw speed.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sonofnos/snaplink/internal/analytics"
	"github.com/sonofnos/snaplink/internal/cache"
	"github.com/sonofnos/snaplink/internal/idgen"
	"github.com/sonofnos/snaplink/internal/metrics"
	"github.com/sonofnos/snaplink/internal/shortcode"
	"github.com/sonofnos/snaplink/internal/store"
)

var customCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type DB interface {
	InsertLink(ctx context.Context, l store.Link) error
	GetLinkByCode(ctx context.Context, code string) (store.Link, error)
	Healthy(ctx context.Context) error
}

type Cache interface {
	GetURL(ctx context.Context, code string) (string, *time.Time, error)
	SetURL(ctx context.Context, code, longURL string, expiresAt *time.Time) error
	GetLiveClicks(ctx context.Context, code string) (int64, error)
	AllowCreate(ctx context.Context, ip string, limit int, window time.Duration) (bool, error)
	Ping(ctx context.Context) error
}

type Handler struct {
	db        DB
	rdb       Cache
	local     *cache.Local
	ids       *idgen.Allocator
	analytics *analytics.Writer
	baseURL   string
	rateLimit int
	logger    *slog.Logger
}

func New(db DB, rdb Cache, local *cache.Local, ids *idgen.Allocator, an *analytics.Writer, baseURL string, rateLimit int, logger *slog.Logger) *Handler {
	return &Handler{db: db, rdb: rdb, local: local, ids: ids, analytics: an, baseURL: strings.TrimRight(baseURL, "/"), rateLimit: rateLimit, logger: logger}
}

// Routes wires the HTTP tree. indexFS, if non-nil, serves the static
// landing page at "/"; chi resolves the literal "/" route before the
// "/{code}" wildcard, so the two never collide.
func (h *Handler) Routes(indexFS http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, chimw.RealIP, chimw.Recoverer, chimw.Timeout(15*time.Second))
	r.Get("/healthz", h.Health)
	r.Get("/readyz", h.Ready)
	r.Handle("/metrics", promhttp.Handler())
	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/links", h.CreateLink)
		api.Get("/links/{code}", h.Stats)
	})
	if indexFS != nil {
		r.Get("/", indexFS.ServeHTTP)
	}
	r.Get("/{code}", h.Redirect)
	return r
}

// --- create ---

type createRequest struct {
	URL        string `json:"url"`
	CustomCode string `json:"custom_code,omitempty"`
	ExpiresIn  int64  `json:"expires_in_seconds,omitempty"`
}

type createResponse struct {
	ShortURL  string     `json:"short_url"`
	Code      string     `json:"code"`
	LongURL   string     `json:"long_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (h *Handler) CreateLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)

	allowed, err := h.rdb.AllowCreate(ctx, ip, h.rateLimit, time.Minute)
	if err != nil {
		h.logger.Error("rate limiter check failed", "error", err)
		// Fail open: an unreachable Redis shouldn't take down link creation.
	} else if !allowed {
		metrics.RateLimitedTotal.Inc()
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again in a minute")
		return
	}

	var req createRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	longURL, err := validateURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	id, err := h.ids.Next(ctx)
	if err != nil {
		h.logger.Error("id allocation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	code := req.CustomCode
	if code != "" {
		if !customCodePattern.MatchString(code) {
			writeError(w, http.StatusBadRequest, "custom_code must be 3-32 characters: letters, digits, - or _")
			return
		}
		if _, err := h.db.GetLinkByCode(ctx, code); err == nil {
			writeError(w, http.StatusConflict, "custom_code already in use")
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			h.logger.Error("lookup failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		code = shortcode.Encode(id)
	}
	idForRow := id

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		expiresAt = &t
	}

	link := store.Link{ID: idForRow, Code: code, LongURL: longURL, CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt}
	if err := h.db.InsertLink(ctx, link); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "custom_code already in use")
			return
		}
		h.logger.Error("insert link failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.rdb.SetURL(ctx, code, longURL, expiresAt); err != nil {
		h.logger.Warn("cache warm failed", "error", err)
	}
	h.local.Set(code, longURL, expiresAt)
	metrics.LinksCreatedTotal.Inc()

	writeJSON(w, http.StatusCreated, createResponse{
		ShortURL:  h.baseURL + "/" + code,
		Code:      code,
		LongURL:   longURL,
		ExpiresAt: expiresAt,
	})
}

// --- redirect (hot path) ---

func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	code := chi.URLParam(r, "code")
	ctx := r.Context()

	longURL, ok := h.local.Get(code)
	if ok {
		metrics.CacheLookups.WithLabelValues("local", "hit").Inc()
	} else {
		metrics.CacheLookups.WithLabelValues("local", "miss").Inc()
		var (
			err error
			exp *time.Time
		)
		longURL, exp, err = h.rdb.GetURL(ctx, code)
		switch {
		case err == nil:
			metrics.CacheLookups.WithLabelValues("redis", "hit").Inc()
			h.local.Set(code, longURL, exp)
		case errors.Is(err, store.ErrNotFound):
			metrics.CacheLookups.WithLabelValues("redis", "miss").Inc()
			link, dbErr := h.db.GetLinkByCode(ctx, code)
			if dbErr != nil {
				metrics.RedirectsTotal.WithLabelValues("not_found").Inc()
				metrics.RedirectDuration.Observe(time.Since(start).Seconds())
				http.NotFound(w, r)
				return
			}
			if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now()) {
				metrics.RedirectsTotal.WithLabelValues("not_found").Inc()
				metrics.RedirectDuration.Observe(time.Since(start).Seconds())
				http.NotFound(w, r)
				return
			}
			longURL = link.LongURL
			_ = h.rdb.SetURL(ctx, code, longURL, link.ExpiresAt)
			h.local.Set(code, longURL, link.ExpiresAt)
		default:
			h.logger.Error("redis lookup failed", "error", err)
			metrics.RedirectsTotal.WithLabelValues("not_found").Inc()
			metrics.RedirectDuration.Observe(time.Since(start).Seconds())
			http.NotFound(w, r)
			return
		}
	}

	h.analytics.Record(store.ClickEvent{
		Code:      code,
		Timestamp: time.Now().UTC(),
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		Referrer:  r.Referer(),
	})

	metrics.RedirectsTotal.WithLabelValues("hit").Inc()
	metrics.RedirectDuration.Observe(time.Since(start).Seconds())
	http.Redirect(w, r, longURL, http.StatusFound)
}

// --- stats ---

type statsResponse struct {
	Code      string     `json:"code"`
	LongURL   string     `json:"long_url"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Clicks    int64      `json:"clicks"`
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	ctx := r.Context()

	link, err := h.db.GetLinkByCode(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "link not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Live clicks (Redis) reflects events not yet flushed to Postgres;
	// use whichever count is higher so stats never regress mid-flush.
	live, _ := h.rdb.GetLiveClicks(ctx, code)
	clicks := link.Clicks
	if live > clicks {
		clicks = live
	}

	writeJSON(w, http.StatusOK, statsResponse{
		Code:      link.Code,
		LongURL:   link.LongURL,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
		Clicks:    clicks,
	})
}

// --- health ---

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Healthy(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := h.rdb.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "cache unavailable")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// --- helpers ---

func validateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url is required")
	}
	if len(raw) > 2048 {
		return "", errors.New("url too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("url must be absolute (include scheme and host)")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("only http and https URLs are allowed")
	}
	return u.String(), nil
}

// clientIP takes the rightmost X-Forwarded-For entry: that's the one the
// platform's own proxy appended. Earlier entries are client-supplied and
// spoofable, which would let a caller rotate them to dodge the rate limit.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
