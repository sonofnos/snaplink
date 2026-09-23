package main

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sonofnos/snaplink/internal/analytics"
	"github.com/sonofnos/snaplink/internal/cache"
	"github.com/sonofnos/snaplink/internal/config"
	"github.com/sonofnos/snaplink/internal/handlers"
	"github.com/sonofnos/snaplink/internal/idgen"
	"github.com/sonofnos/snaplink/internal/store"
)

//go:embed web
var webFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.Join(errors.New("connect postgres"), err)
	}
	defer db.Close()

	if err := db.Migrate(ctx, store.Schema); err != nil {
		return errors.Join(errors.New("run migrations"), err)
	}

	redisOpts, err := store.ParseRedisURL(cfg.RedisURL)
	if err != nil {
		return errors.Join(errors.New("parse redis url"), err)
	}
	rdb := store.NewRedis(redisOpts, cfg.LocalCacheTTL*10) // Redis TTL a bit longer than local TTL
	defer rdb.Close()
	if err := rdb.Ping(ctx); err != nil {
		return errors.Join(errors.New("connect redis"), err)
	}

	local := cache.New(cfg.LocalCacheSize, cfg.LocalCacheTTL)
	ids := idgen.New(db, cfg.IDBlockSize)
	an := analytics.New(db, rdb, cfg.AnalyticsBuffer, cfg.AnalyticsFlush, logger)

	analyticsCtx, cancelAnalytics := context.WithCancel(context.Background())
	defer cancelAnalytics()
	go an.Run(analyticsCtx)

	h := handlers.New(db, rdb, local, ids, an, cfg.BaseURL, cfg.RateLimitPerMin, logger)

	webRoot, err := webRootHandler()
	if err != nil {
		return err
	}

	r := h.Routes(webRoot)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "port", cfg.Port, "base_url", cfg.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// webRootHandler serves the embedded landing page at exactly "/".
func webRootHandler() (http.Handler, error) {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}), nil
}
