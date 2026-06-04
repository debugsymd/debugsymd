package httpapi

import (
	"fmt"
	"io"
	"net/http"
)

// statusRecorder wraps a http.ResponseWriter to capture the status code that
// http.ServeContent ultimately writes. ServeContent resolves the real outcome
// itself — 200, 206 for a satisfiable Range, or 304 for a conditional hit — so
// the served-path metric must read the code back rather than assume 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	// Default to 200: a handler that writes a body without an explicit WriteHeader
	// still produces a 200, matching net/http's own behavior.
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// ReadFrom preserves the underlying ResponseWriter's io.ReaderFrom fast path so
// http.ServeContent can still stream a cached *os.File via sendfile (zero-copy)
// — without this, wrapping the writer would silently force every multi-GB
// payload through a userspace buffer copy. WriteHeader has already run by the
// time ServeContent copies the body, so the captured status is unaffected.
func (s *statusRecorder) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		if err != nil {
			return n, fmt.Errorf("streaming response body: %w", err)
		}

		return n, nil
	}

	n, err := io.Copy(s.ResponseWriter, r)
	if err != nil {
		return n, fmt.Errorf("copying response body: %w", err)
	}

	return n, nil
}
