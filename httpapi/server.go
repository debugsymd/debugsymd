// Package httpapi is the HTTP front end: it speaks the Microsoft symsrv path
// convention and the debuginfod protocol on a public listener, and exposes
// metrics and health on a separate admin listener.
package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/debugsymd/debugsymd/metrics"
)

const (
	shutdownGrace     = 10 * time.Second
	readHeaderTimeout = 15 * time.Second
)

// Server runs the public symstore listener and the admin (metrics/health)
// listener together.
type Server struct {
	public *http.Server
	admin  *http.Server
}

// NewServer builds the two listeners. ready reports whether the daemon is ready
// to serve (used by /readyz); a nil ready is treated as always ready. When
// debuginfod is true, the build-id debuginfod routes are also served.
func NewServer(bind, admin string, h *Handler, ready func() bool, debuginfod bool) *Server {
	public := http.NewServeMux()
	// A GET pattern also matches HEAD in ServeMux, and ServeContent emits a
	// bodyless HEAD response, so one registration covers both.
	public.HandleFunc("GET /{leading}/{signature}/{trailing}", withMetrics(symstoreForm, h.symstoreRoute))

	if debuginfod {
		// Literal final segments keep these from shadowing the 3-segment symstore route.
		public.HandleFunc("GET /buildid/{buildid}/debuginfo", withMetrics(fixedForm("debuginfo"), h.debuginfodDebug))
		public.HandleFunc("GET /buildid/{buildid}/executable", withMetrics(fixedForm("executable"), h.debuginfodExecutable))
		public.HandleFunc("GET /buildid/{buildid}/source/{srcpath...}", withMetrics(fixedForm("source"), h.debuginfodSource))
	}

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	adminMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready != nil && !ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}

		_, _ = io.WriteString(w, "ready\n")
	})
	adminMux.Handle("GET /metrics", metrics.Handler())

	return &Server{
		public: &http.Server{Addr: bind, Handler: trackInFlight(public), ReadHeaderTimeout: readHeaderTimeout},
		admin:  &http.Server{Addr: admin, Handler: adminMux, ReadHeaderTimeout: readHeaderTimeout},
	}
}

// Run starts both listeners and blocks until ctx is cancelled or a listener
// fails, then shuts both down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errc := make(chan error, 2)

	go func() { errc <- s.public.ListenAndServe() }()
	go func() { errc <- s.admin.ListenAndServe() }()

	var runErr error

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	// Keep ctx's values but drop its cancellation so the drain gets its own deadline.
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()

	_ = s.public.Shutdown(shutCtx)
	_ = s.admin.Shutdown(shutCtx)

	return runErr
}
