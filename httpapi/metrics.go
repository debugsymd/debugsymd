package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/debugsymd/debugsymd/metrics"
)

// formLabeler picks the bounded "form" metric label for a request.
type formLabeler func(*http.Request) string

func withMetrics(form formLabeler, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		label := form(r)
		rec := newStatusRecorder(w)

		next(rec, r)

		metrics.RequestDuration.WithLabelValues(label).Observe(time.Since(start).Seconds())
		metrics.RequestsTotal.WithLabelValues(strconv.Itoa(rec.status), label).Inc()
	}
}

// symstoreForm derives the label from the client-controlled trailing segment.
func symstoreForm(r *http.Request) string {
	return form(r.PathValue("trailing"))
}

// fixedForm builds a labeler that reports the same form for every request.
func fixedForm(label string) formLabeler {
	return func(*http.Request) string { return label }
}

// trackInFlight wraps a handler so the requests_in_flight gauge reflects the
// number of requests currently being served.
func trackInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.RequestsInFlight.Inc()
		defer metrics.RequestsInFlight.Dec()

		next.ServeHTTP(w, r)
	})
}
