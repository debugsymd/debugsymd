// Package metrics defines the Prometheus collectors debugsymd exposes on the
// admin listener's /metrics endpoint, plus the standard Go runtime and process
// collectors. Subsystems import this package and instrument inline; the registry
// and HTTP handler live here so there is a single registration point.
package metrics

import (
	"net/http"
	"runtime"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "debugsymd"

// Result label values shared across counters.
const (
	ResultHit      = "hit"
	ResultMiss     = "miss"
	ResultOK       = "ok"
	ResultNotFound = "not_found"
	ResultError    = "error"
)

// Label names shared across collectors.
const (
	labelStatus = "status"
	labelForm   = "form"
	labelRole   = "role"
	labelResult = "result"
)

// registry is debugsymd's own registry rather than the global default, so the
// exposition contains exactly the collectors registered below — no surprises
// from libraries that self-register on the default registry. It is built by
// newRegistry so registration happens at package initialization without an
// init function; Go orders this after the collector vars it references.
var registry = newRegistry()

var (
	// RequestsTotal counts handled requests by HTTP status and symbol form.
	RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "requests_total",
		Help:      "Symbol requests handled, by HTTP status and symbol form.",
	}, []string{labelStatus, labelForm})

	// RequestDuration is the end-to-end serve time of a symbol request, by form.
	RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "request_duration_seconds",
		Help:      "Symbol request serve time in seconds, by symbol form.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{labelForm})

	// RequestsInFlight tracks symbol requests currently being served.
	RequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "requests_in_flight",
		Help:      "Symbol requests currently being served.",
	})

	// CacheLookups counts disk-cache lookups by logical role and hit/miss.
	CacheLookups = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_lookups_total",
		Help:      "Disk-cache lookups, by role and result (hit/miss).",
	}, []string{labelRole, labelResult})

	// CacheSizeBytes is the total size of committed cache entries on disk.
	CacheSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cache_size_bytes",
		Help:      "Total bytes of committed cache entries on disk.",
	})

	// CacheEntries is the number of committed cache entries on disk.
	CacheEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cache_entries",
		Help:      "Number of committed cache entries on disk.",
	})

	// CacheEvictedTotal counts entries removed by the eviction sweeper.
	CacheEvictedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_evicted_total",
		Help:      "Cache entries removed by the eviction sweeper.",
	})

	// CacheEvictionDuration is the wall-clock time of an eviction sweep.
	CacheEvictionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "cache_eviction_duration_seconds",
		Help:      "Eviction sweep duration in seconds.",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	})

	// CacheLastEvictionTimestamp is the Unix time of the last completed sweep.
	CacheLastEvictionTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cache_last_eviction_timestamp_seconds",
		Help:      "Unix timestamp of the last completed eviction sweep.",
	})

	// StorageBytesDownloaded counts bytes streamed from the blob store.
	StorageBytesDownloaded = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "storage_bytes_downloaded_total",
		Help:      "Bytes streamed from the blob store.",
	})

	// StorageFetchDuration is the latency of a blob-store fetch.
	StorageFetchDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "storage_fetch_duration_seconds",
		Help:      "Blob-store fetch latency in seconds.",
		Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
	})

	// StorageErrorsTotal counts blob-store fetch failures (not key-absent).
	StorageErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "storage_errors_total",
		Help:      "Blob-store fetch failures, excluding key-not-found.",
	})

	// ResolverRequests counts resolver lookups by result (ok/not_found/error).
	ResolverRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "resolver_requests_total",
		Help:      "Resolver lookups, by result (ok/not_found/error).",
	}, []string{labelResult})

	// ResolverDuration is the latency of a resolver lookup.
	ResolverDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "resolver_duration_seconds",
		Help:      "Resolver lookup latency in seconds.",
		Buckets:   prometheus.DefBuckets,
	})

	// SingleflightCoalescedTotal counts requests served by a shared in-flight
	// download or synthesis rather than doing their own backend work.
	SingleflightCoalescedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "singleflight_coalesced_total",
		Help:      "Requests served by a shared in-flight download or synthesis.",
	})

	// CABSynthDuration is the time spent synthesizing a CAB envelope.
	CABSynthDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "cab_synth_duration_seconds",
		Help:      "CAB synthesis time in seconds.",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	})

	// CABSynthBytesTotal counts uncompressed bytes fed into CAB synthesis.
	CABSynthBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cab_synth_bytes_total",
		Help:      "Uncompressed bytes fed into CAB synthesis.",
	})

	// BuildInfo is a constant 1 carrying version/revision/goversion labels.
	BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build information; the value is always 1.",
	}, []string{"version", "revision", "goversion"})
)

// newRegistry creates the registry and registers every collector debugsymd
// exposes, including the standard Go runtime and process collectors. It is the
// single registration point, called once to initialize the package-level
// registry var.
func newRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		RequestsTotal,
		RequestDuration,
		RequestsInFlight,
		CacheLookups,
		CacheSizeBytes,
		CacheEntries,
		CacheEvictedTotal,
		CacheEvictionDuration,
		CacheLastEvictionTimestamp,
		StorageBytesDownloaded,
		StorageFetchDuration,
		StorageErrorsTotal,
		ResolverRequests,
		ResolverDuration,
		SingleflightCoalescedTotal,
		CABSynthDuration,
		CABSynthBytesTotal,
		BuildInfo,
	)

	setBuildInfo()

	return r
}

// BuildMetadata is the binary's version information, derived from the build info
// that `go build` embeds (module version and VCS revision via buildvcs).
type BuildMetadata struct {
	Version   string
	Revision  string
	GoVersion string
}

// ReadBuild returns the binary's build metadata, falling back to "unknown" for
// any field the build did not embed. It is the single source for both the
// build_info metric and the startup log line.
func ReadBuild() BuildMetadata {
	const unknown = "unknown"

	b := BuildMetadata{Version: unknown, Revision: unknown, GoVersion: runtime.Version()}

	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" {
			b.Version = bi.Main.Version
		}

		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				b.Revision = s.Value
			}
		}
	}

	return b
}

// setBuildInfo publishes the build_info series from the embedded build metadata.
func setBuildInfo() {
	b := ReadBuild()
	BuildInfo.WithLabelValues(b.Version, b.Revision, b.GoVersion).Set(1)
}

// Handler serves the registered collectors in the Prometheus exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
