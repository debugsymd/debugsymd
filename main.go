// Command debugsymd is a headless Microsoft symsrv proxy: it fronts a Sentry
// (S3-backed) symbol store and serves both the uncompressed (.pdb/.dll/.exe)
// and Microsoft-compressed (.pd_/.dl_/.ex_, CAB/MSZIP) symbol forms to Windows
// debuggers pointed at it via _NT_SYMBOL_PATH.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/debugsymd/debugsymd/config"
	"github.com/debugsymd/debugsymd/diskcache"
	"github.com/debugsymd/debugsymd/httpapi"
	"github.com/debugsymd/debugsymd/objects"
	"github.com/debugsymd/debugsymd/resolver"
	"github.com/debugsymd/debugsymd/storage"
)

const evictionInterval = time.Hour

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("debugsymd exited", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	setupLogging(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cache, cacheErr := diskcache.New(cfg.CacheDir)
	if cacheErr != nil {
		return fmt.Errorf("initializing cache: %w", cacheErr)
	}

	// Seed the cache gauges off the critical path: the walk can be slow on a large
	// warm cache, so it runs concurrently with the storage/resolver setup below.
	// We join on it before serving (and well before the first eviction sweep), so
	// the seed's gauge Set happens-before any Commit or eviction delta.
	seeded := make(chan struct{})

	go func() {
		defer close(seeded)

		cache.SeedMetrics()
	}()

	go cache.RunEviction(ctx, evictionInterval, cfg.CacheMaxUnusedFor)

	fetcher, fetcherErr := storage.NewS3(ctx, storage.S3Options{
		Region:      cfg.Storage.Region,
		EndpointURL: cfg.Storage.EndpointURL,
	})
	if fetcherErr != nil {
		return fmt.Errorf("initializing storage: %w", fetcherErr)
	}

	res := resolver.NewSentry(resolver.SentryOptions{
		APIURL:    cfg.Sentry.APIURL,
		Org:       cfg.Sentry.Org,
		Project:   cfg.Sentry.Project,
		Token:     cfg.Sentry.Token,
		Bucket:    cfg.Storage.Bucket,
		KeyPrefix: cfg.Storage.KeyPrefix,
	})

	svc := objects.New(res, fetcher, cache)
	handler := httpapi.NewHandler(svc)
	// Readiness reflects a real precondition for serving: the on-disk cache must
	// be writable (every served object is staged through it).
	ready := func() bool { return cache.Probe() == nil }
	server := httpapi.NewServer(cfg.Bind, cfg.Admin, handler, ready, cfg.Debuginfod)

	// Block until the gauges are seeded so no request-driven Commit delta is
	// clobbered by a late seed Set.
	<-seeded

	// #nosec G706 -- operator-supplied config logged as discrete slog fields
	// (JSON-encoded), not request data spliced into a format string.
	slog.Info("debugsymd starting",
		"bind", cfg.Bind,
		"admin", cfg.Admin,
		"cache_dir", cfg.CacheDir,
		"bucket", cfg.Storage.Bucket,
		"debuginfod", cfg.Debuginfod,
	)

	if err := server.Run(ctx); err != nil {
		return fmt.Errorf("running server: %w", err)
	}

	return nil
}

func setupLogging(level string) {
	var lvl slog.Level

	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
