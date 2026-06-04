// Package diskcache is the content-addressed, sharded on-disk store backing the
// proxy's object / raw_compressed / cab_synth roles. Entries are written to a
// staging file and atomically renamed into place, and lookups stat the disk on
// every call so a negative result is never remembered in memory - a concurrent
// request may have populated the entry since.
package diskcache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Cache is a handle to a cache root directory. It is safe for concurrent use:
// all state lives on the filesystem.
type Cache struct {
	root string
}

// New prepares the cache root and its staging directory.
func New(root string) (*Cache, error) {
	c := &Cache{root: root}
	if err := os.MkdirAll(filepath.Join(root, tmpSubdir), dirPerm); err != nil {
		return nil, fmt.Errorf("creating cache staging dir: %w", err)
	}

	return c, nil
}

// Lookup opens the cache entry for (role, debugID, inner) if it exists. A miss
// returns (nil, nil, nil) — never an error — and is deliberately not cached, so
// the next call re-checks the disk. On a hit the entry's timestamps are touched
// (best effort) so the eviction sweeper treats it as recently used. The caller
// owns the returned file and must close it.
func (c *Cache) Lookup(role, debugID, inner string) (*os.File, fs.FileInfo, error) {
	p := c.path(role, debugID, inner)
	// #nosec G304 -- p is a sha256-addressed path under the cache root; the hex
	// hash has no separators, so no traversal is possible.
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("opening cache entry: %w", err)
	}

	fi, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("stat cache entry: %w", statErr)
	}

	now := time.Now()
	_ = os.Chtimes(p, now, now)

	return f, fi, nil
}

// NewTemp creates a staging file in the cache's tmp directory, on the same
// filesystem as the final entries so Commit's rename is atomic. If the caller
// does not Commit it, it must remove the file (os.Remove(f.Name())).
func (c *Cache) NewTemp() (*os.File, error) {
	f, err := os.CreateTemp(filepath.Join(c.root, tmpSubdir), tmpPattern)
	if err != nil {
		return nil, fmt.Errorf("creating staging file: %w", err)
	}

	return f, nil
}

// Probe reports whether the cache is writable by creating and removing a staging
// file. It backs the readiness check: a daemon whose cache volume is unmounted,
// read-only, or full cannot serve, and should fail /readyz rather than accept
// traffic it will only 503.
func (c *Cache) Probe() error {
	f, err := c.NewTemp()
	if err != nil {
		return err
	}

	name := f.Name()
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing probe file: %w", closeErr)
	}

	if rmErr := os.Remove(name); rmErr != nil {
		return fmt.Errorf("removing probe file: %w", rmErr)
	}

	return nil
}

// Commit fsyncs the staging file, closes it, and renames it into the entry's
// final sharded location. After a successful Commit the staging file no longer
// exists.
func (c *Cache) Commit(tmp *os.File, role, debugID, inner string) error {
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("fsyncing staging file: %w", err)
	}

	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing staging file: %w", err)
	}

	final := c.path(role, debugID, inner)
	if err := os.MkdirAll(filepath.Dir(final), dirPerm); err != nil {
		return fmt.Errorf("creating cache shard dir: %w", err)
	}

	if err := os.Rename(name, final); err != nil {
		return fmt.Errorf("committing cache entry: %w", err)
	}

	return nil
}
