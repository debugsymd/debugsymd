package diskcache

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/debugsymd/debugsymd/metrics"
)

// Evict removes every cache file whose modification time is older than
// maxUnused and returns the number removed. Lookup touches an entry's timestamp
// on each hit, so "modified long ago" means "not served recently". Staging
// files left behind by interrupted writes are swept the same way. After the file
// sweep it prunes the now-empty shard directories so a long-lived, high-churn
// cache does not accumulate empty <role>/<hh>/<hh> trees.
func (c *Cache) Evict(maxUnused time.Duration) (int, error) {
	start := time.Now()
	cutoff := start.Add(-maxUnused)

	var (
		removed        int
		removedEntries int64
		removedBytes   int64
		dirs           []string
	)

	walkErr := c.walkFiles(func(path string, info fs.FileInfo, inTmp bool) {
		// Only entries past the cutoff are swept.
		if !info.ModTime().Before(cutoff) {
			return
		}
		// #nosec G122 -- the cache root is operator-owned, holding only files debugsymd
		// writes itself (sha256-named, no symlinks), so there is no TOCTOU symlink to race.
		if os.Remove(path) != nil {
			return
		}

		removed++

		// Staging files under tmp were never counted in the size/entry gauges,
		// so only committed entries adjust them on removal.
		if !inTmp {
			removedEntries++
			removedBytes += info.Size()
		}
	}, func(path string) {
		// The root and the tmp staging dir are skipped by walkFiles; every other
		// directory is a prune candidate once its entries are gone.
		dirs = append(dirs, path)
	})
	if walkErr != nil {
		return removed, walkErr
	}

	pruneEmptyDirs(dirs)

	metrics.CacheSizeBytes.Sub(float64(removedBytes))
	metrics.CacheEntries.Sub(float64(removedEntries))
	metrics.CacheEvictedTotal.Add(float64(removedEntries))
	metrics.CacheEvictionDuration.Observe(time.Since(start).Seconds())
	metrics.CacheLastEvictionTimestamp.SetToCurrentTime()

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
