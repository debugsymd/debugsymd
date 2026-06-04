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
	"os"

	"golang.org/x/sync/singleflight"

	"github.com/debugsymd/debugsymd/diskcache"
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

	if file, info, err := s.cache.Lookup(roleObjects, id, inner); err != nil {
		return nil, nil, fmt.Errorf("looking up cached object: %w", err)
	} else if file != nil {
		return file, info, nil
	}

	// singleflight runs the closure under the first caller's context, so a single
	// client disconnect would otherwise cancel the shared download and fail every
	// coalesced follower too. Drop cancellation (keeping any context values) so
	// the work is bound to the symbol, not to one requester.
	work := context.WithoutCancel(ctx)

	if _, err, _ := s.group.Do(flightKey(roleObjects, id, inner), func() (any, error) {
		return nil, s.download(work, req)
	}); err != nil {
		return nil, nil, fmt.Errorf("downloading object: %w", err)
	}

	file, info, err := s.cache.Lookup(roleObjects, id, inner)
	if err != nil {
		return nil, nil, fmt.Errorf("opening downloaded object: %w", err)
	}

	return file, info, nil
}

// download resolves the object's location, streams it from the blob store into
// a staging file, and commits it to the objects cache. It streams throughout —
// a multi-GB object is never held in memory.
func (s *Service) download(ctx context.Context, req resolver.Request) error {
	loc, err := s.resolver.Lookup(ctx, req)
	if errors.Is(err, resolver.ErrNotFound) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("resolving %q: %w", req.Filename, err)
	}

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

	if _, err := s.fetcher.Fetch(ctx, loc.Bucket, loc.Key, tmp); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrNotFound
		}

		return fmt.Errorf("fetching object bytes: %w", err)
	}

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
