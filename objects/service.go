// Package objects is the proxy's core: it ties the resolver, blob store, disk
// cache, and CAB synthesizer together behind two operations — fetch the raw
// object, or fetch a CAB-wrapped object. Concurrent requests for the same entry
// are collapsed to a single download/synthesis with singleflight.
package objects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/debugsymd/debugsymd/diskcache"
	"github.com/debugsymd/debugsymd/metrics"
	"github.com/debugsymd/debugsymd/resolver"
	"github.com/debugsymd/debugsymd/storage"
)

// ErrNotFound reports that no object matches the request. It folds together the
// resolver's and storage's not-found cases so callers test one error.
var ErrNotFound = errors.New("objects: not found")

// Service produces object and CAB bytes, caching both on disk.
type Service struct {
	resolver resolver.Resolver
	fetcher  storage.Fetcher
	cache    *diskcache.Cache
	group    singleflight.Group
}

func New(r resolver.Resolver, f storage.Fetcher, c *diskcache.Cache) *Service {
	return &Service{resolver: r, fetcher: f, cache: c}
}

// Fetch returns the verbatim object bytes for req, downloading and caching them
// on a miss. The caller owns the returned file.
func (s *Service) Fetch(ctx context.Context, req resolver.Request) (*os.File, fs.FileInfo, error) {
	return s.object(ctx, req)
}

// object is the cached, singleflight-protected accessor for the raw object. On
// a miss exactly one caller downloads; the rest wait and then open their own
// handle to the freshly committed entry.
func (s *Service) object(ctx context.Context, req resolver.Request) (*os.File, fs.FileInfo, error) {
	id, inner := cacheKey(req)

	file, info, err := s.cache.Lookup(roleObjects, id, inner)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up cached object: %w", err)
	}

	if file != nil {
		metrics.CacheLookups.WithLabelValues(roleObjects, metrics.ResultHit).Inc()
		slog.InfoContext(ctx, "object cache hit", "id", id, "inner", inner)

		return file, info, nil
	}

	metrics.CacheLookups.WithLabelValues(roleObjects, metrics.ResultMiss).Inc()
	slog.InfoContext(ctx, "object cache miss, downloading", "id", id, "inner", inner)

	if err := s.produceOnce(ctx, flightKey(roleObjects, id, inner), func(ctx context.Context) error {
		return s.download(ctx, req)
	}); err != nil {
		return nil, nil, err
	}

	// Re-open the freshly committed entry. This always hits and is not counted
	// as a lookup — the hit/miss above already classified the request.
	file, info, err = s.cache.Lookup(roleObjects, id, inner)
	if err != nil {
		return nil, nil, fmt.Errorf("opening downloaded object: %w", err)
	}

	return file, info, nil
}

// produceOnce runs produce under singleflight keyed by flightKey, coalescing
// concurrent callers for the same key into a single execution. It records a
// coalesced request for every caller that was served by another caller's
// in-flight work — that is, callers whose produce closure never ran. The
// executor that actually did the backend work is never counted, even though
// singleflight reports shared=true to it once duplicates exist.
//
// The closure runs under a context with cancellation dropped (values kept): the
// shared work is bound to the symbol, not to the first requester, so one client
// disconnecting must not cancel the download and fail every coalesced follower.
func (s *Service) produceOnce(ctx context.Context, key string, produce func(context.Context) error) error {
	work := context.WithoutCancel(ctx)

	executed := false

	_, err, _ := s.group.Do(key, func() (any, error) {
		executed = true
		return nil, produce(work)
	})
	if err != nil {
		return fmt.Errorf("producing cache entry: %w", err)
	}

	if !executed {
		metrics.SingleflightCoalescedTotal.Inc()
	}

	return nil
}

// download resolves the object's location, streams it from the blob store into
// a staging file, and commits it to the objects cache. It streams throughout —
// a multi-GB object is never held in memory.
func (s *Service) download(ctx context.Context, req resolver.Request) error {
	resolveStart := time.Now()
	loc, err := s.resolver.Lookup(ctx, req)
	resolveSeconds := time.Since(resolveStart).Seconds()

	if errors.Is(err, resolver.ErrNotFound) {
		metrics.ResolverRequests.WithLabelValues(metrics.ResultNotFound).Inc()
		slog.InfoContext(ctx, "resolver: object not found", "filename", req.Filename)

		return ErrNotFound
	}

	if err != nil {
		metrics.ResolverRequests.WithLabelValues(metrics.ResultError).Inc()
		return fmt.Errorf("resolving %q: %w", req.Filename, err)
	}

	// Observe only successful lookups: a not-found (fast, common) or an error
	// (e.g. a slow timeout) would otherwise skew the success-latency percentiles
	// the histogram exists to track. The result counts live in ResolverRequests.
	metrics.ResolverRequests.WithLabelValues(metrics.ResultOK).Inc()
	metrics.ResolverDuration.Observe(resolveSeconds)

	slog.InfoContext(ctx, "resolved object",
		"filename", req.Filename,
		"bucket", loc.Bucket,
		"key", loc.Key,
		"size", loc.Size,
		"resolve_seconds", resolveSeconds,
	)

	tmp, tmpErr := s.cache.NewTemp()
	if tmpErr != nil {
		return fmt.Errorf("creating staging file: %w", tmpErr)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	fetchStart := time.Now()
	n, fetchErr := s.fetcher.Fetch(ctx, loc.Bucket, loc.Key, tmp)
	fetchSeconds := time.Since(fetchStart).Seconds()

	if fetchErr != nil {
		// A missing key is an upstream inconsistency, not a backend fault, so it
		// is mapped to 404 and excluded from the storage error counter.
		if errors.Is(fetchErr, storage.ErrNotFound) {
			return ErrNotFound
		}

		metrics.StorageErrorsTotal.Inc()

		return fmt.Errorf("fetching object bytes: %w", fetchErr)
	}

	// Observe only successful, complete fetches so a partial transfer that
	// errored out does not distort the download-latency percentiles.
	metrics.StorageFetchDuration.Observe(fetchSeconds)
	metrics.StorageBytesDownloaded.Add(float64(n))

	slog.InfoContext(ctx, "fetched object bytes",
		"key", loc.Key,
		"bytes", n,
		"fetch_seconds", fetchSeconds,
	)

	id, inner := cacheKey(req)
	if err := s.cache.Commit(tmp, roleObjects, id, inner); err != nil {
		return fmt.Errorf("committing object to cache: %w", err)
	}

	committed = true

	return nil
}

// cacheKey derives the disk-cache (id, inner) pair for req. The identity is the
// debug ID when present, else the code ID. The inner key is prefixed with the
// file type so distinct DIFs sharing one identity (e.g. elf_code vs elf_debug
// for one build-id) never collide.
func cacheKey(req resolver.Request) (id, inner string) {
	return cmp.Or(req.DebugID, req.CodeID), string(req.FileType) + "/" + req.Filename
}

// flightKey namespaces a singleflight call by role and identity.
func flightKey(role, id, inner string) string {
	return role + "|" + id + "|" + inner
}
