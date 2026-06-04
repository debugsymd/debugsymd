package httpapi

import (
	"expvar"
	"strconv"
	"strings"
)

// requestsTotal counts handled requests keyed by "<status>:<form>", e.g.
// "200:pd_" or "404:pdb". Exposed as JSON on the admin listener's /metrics.
var requestsTotal = expvar.NewMap("debugsymd_requests_total")

func recordRequest(status int, form string) {
	var b strings.Builder
	b.Grow(len(form) + 4)
	b.WriteString(strconv.Itoa(status))
	b.WriteByte(':')
	b.WriteString(form)
	requestsTotal.Add(b.String(), 1)
}
