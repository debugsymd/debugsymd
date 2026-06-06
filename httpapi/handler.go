package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/debugsymd/debugsymd/objects"
	"github.com/debugsymd/debugsymd/resolver"
	"github.com/debugsymd/debugsymd/symstore"
)

// retryAfterSeconds is sent with a 503 when a backend lookup fails transiently,
// so debuggers back off and retry. Content types and the compressed-extension
// table live in the symstore package, which owns the path grammar.
const retryAfterSeconds = "5"

// Objects is the subset of the object service the HTTP layer needs. The
// concrete *objects.Service satisfies it.
type Objects interface {
	Fetch(ctx context.Context, req resolver.Request) (*os.File, fs.FileInfo, error)
	FetchCompressed(ctx context.Context, req resolver.Request) (*os.File, fs.FileInfo, error)
	SourceFile(ctx context.Context, req resolver.Request, sourcePath string) ([]byte, error)
}

// Handler serves the symsrv path convention `/<filename>/<signature>/<filename>`.
type Handler struct {
	objects Objects
}

func NewHandler(o Objects) *Handler {
	return &Handler{objects: o}
}

// symstoreRoute handles a single symbol request. The leading and trailing
// filenames must match (Microsoft's convention); symstore.Parse turns the
// signature into a resolver request plus a serving decision (verbatim object,
// CAB envelope, or source-bundle zip). http.ServeContent gives us HEAD, Range,
// Content-Length, and conditional handling for free.
func (h *Handler) symstoreRoute(w http.ResponseWriter, r *http.Request) {
	leading := r.PathValue("leading")
	signature := r.PathValue("signature")
	trailing := r.PathValue("trailing")

	if !strings.EqualFold(leading, trailing) {
		http.Error(w, "leading and trailing filenames differ", http.StatusBadRequest)
		return
	}

	req, serve, ok := symstore.Parse(leading, signature, trailing)
	if !ok {
		http.NotFound(w, r)
		return
	}

	fetch := h.objects.Fetch
	if serve.CAB {
		fetch = h.objects.FetchCompressed
	}

	file, info, err := fetch(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if file == nil {
		// nil file with no error shouldn't happen; treat as a miss, don't deref.
		h.fail(w, r, objects.ErrNotFound)
		return
	}

	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", serve.ContentType)
	// w is the statusRecorder from withMetrics, so ServeContent's 200/206/304 is
	// captured for the request metric without a local wrapper here.
	http.ServeContent(w, r, req.Filename, info.ModTime(), file)
}

// fail maps a lookup error to a response: 404 for a miss, 503 (with Retry-After)
// for a transient backend failure so debuggers back off rather than give up.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, objects.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	// #nosec G706 -- path is a discrete slog field (JSON-encoded), not interpolated
	// into a format string, so it cannot forge log entries.
	slog.Error("symbol lookup failed", "error", err, "path", r.URL.Path)
	w.Header().Set("Retry-After", retryAfterSeconds)
	http.Error(w, "symbol backend unavailable", http.StatusServiceUnavailable)
}
