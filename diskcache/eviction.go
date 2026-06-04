package diskcache

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Evict removes every cache file whose modification time is older than
// maxUnused and returns the number removed. Lookup touches an entry's timestamp
// on each hit, so "modified long ago" means "not served recently". Staging
// files left behind by interrupted writes are swept the same way. After the file
// sweep it prunes the now-empty shard directories so a long-lived, high-churn
// cache does not accumulate empty <role>/<hh>/<hh> trees.
func (c *Cache) Evict(maxUnused time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxUnused)
	tmpDir := filepath.Join(c.root, tmpSubdir)

	var (
		removed int
		dirs    []string
	)

	walkErr := filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			// The root and the tmp staging dir are structural — never prune them;
			// tmp in particular must exist for NewTemp to stage future writes.
			if path != c.root && path != tmpDir {
				dirs = append(dirs, path)
			}

			return nil
		}
		// d.Info errors only if the entry vanished (raced another remover): nothing to evict.
		if info, infoErr := d.Info(); infoErr == nil && info.ModTime().Before(cutoff) {
			// #nosec G122 -- the cache root is operator-owned, holding only files debugsymd
			// writes itself (sha256-named, no symlinks), so there is no TOCTOU symlink to race.
			if os.Remove(path) == nil {
				removed++
			}
		}

		return nil
	})
	if walkErr != nil {
		return removed, fmt.Errorf("walking cache dir: %w", walkErr)
	}

	pruneEmptyDirs(dirs)

	return removed, nil
}

// pruneEmptyDirs removes empty directories deepest-first. A child path is always
// longer than its parent, so descending by path length guarantees a directory is
// attempted before its ancestors; os.Remove only succeeds on an empty directory,
// so non-empty shards are left intact. Sorted with slices.SortFunc, never an
// ad-hoc sort.
func pruneEmptyDirs(dirs []string) {
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })

	for _, dir := range dirs {
		// #nosec G104 -- a non-empty (still-in-use) directory simply fails to remove;
		// that is the intended guard, not an error to surface.
		_ = os.Remove(dir)
	}
}

// RunEviction sweeps the cache every interval until ctx is cancelled.
func (c *Cache) RunEviction(ctx context.Context, interval, maxUnused time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := c.Evict(maxUnused)
			if err != nil {
				slog.Warn("cache eviction sweep failed", "error", err)
				continue
			}

			if removed > 0 {
				slog.Info("cache eviction sweep", "removed", removed)
			}
		}
	}
}
