package diskcache

import (
	"io/fs"

	"github.com/debugsymd/debugsymd/metrics"
)

// SeedMetrics seeds the cache_size_bytes / cache_entries gauges from the entries
// left by a previous run so they are accurate before the first Commit or eviction
// adjusts them.
func (c *Cache) SeedMetrics() {
	if bytes, entries, err := c.computeSize(); err == nil {
		metrics.CacheSizeBytes.Set(float64(bytes))
		metrics.CacheEntries.Set(float64(entries))
	}
}

// computeSize walks the cache and returns the total bytes and number of
// committed entry files, skipping the tmp staging directory.
func (c *Cache) computeSize() (bytes, entries int64, err error) {
	walkErr := c.walkFiles(func(_ string, info fs.FileInfo, inTmp bool) {
		if inTmp {
			return
		}

		bytes += info.Size()
		entries++
	}, nil)

	return bytes, entries, walkErr
}
