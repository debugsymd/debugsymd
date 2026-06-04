package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	dsymsPath          = "files/dsyms/"
	defaultHTTPTimeout = 30 * time.Second
	minSHA1Len         = 4

	symbolTypeSourceBundle = "sourcebundle"
)

// Sentry resolves identifiers through the Sentry REST API
// (GET /projects/{org}/{project}/files/dsyms/?debug_id=…&code_id=…), then derives
// the S3 blob key from the returned SHA-1 checksum.
//
// The key derivation (see storageKey) assumes Sentry's default S3/GCS filestore
// layout, which shards blobs by the first two checksum byte-pairs. Deployments
// that customize the filestore path can adjust KeyPrefix or this function — it
// is the single point that encodes the storage layout.
type Sentry struct {
	client    *http.Client
	base      string
	org       string
	project   string
	token     string
	bucket    string
	keyPrefix string
}

// SentryOptions configures a Sentry resolver. Client is optional.
type SentryOptions struct {
	APIURL    string
	Org       string
	Project   string
	Token     string
	Bucket    string
	KeyPrefix string
	Client    *http.Client
}

func NewSentry(o SentryOptions) *Sentry {
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return &Sentry{
		client:    client,
		base:      strings.TrimRight(o.APIURL, "/"),
		org:       o.Org,
		project:   o.Project,
		token:     o.Token,
		bucket:    o.Bucket,
		keyPrefix: o.KeyPrefix,
	}
}

// debugFile is the subset of Sentry's debug-file serializer we consume. The
// exact shape (symbolType vocabulary, data.features) is a deployment assumption
// — see the package plan's "open items".
type debugFile struct {
	DebugID    string `json:"debugId"`
	CodeID     string `json:"codeId"`
	ObjectName string `json:"objectName"`
	SymbolType string `json:"symbolType"`
	SHA1       string `json:"sha1"`
	Size       int64  `json:"size"`
	Data       struct {
		Features []string `json:"features"`
	} `json:"data"`
}

func (s *Sentry) Lookup(ctx context.Context, req Request) (*Location, error) {
	endpoint, err := url.JoinPath(s.base, "projects", s.org, s.project, dsymsPath)
	if err != nil {
		return nil, fmt.Errorf("building sentry url: %w", err)
	}

	q := url.Values{}
	if req.DebugID != "" {
		q.Set("debug_id", req.DebugID)
	}

	if req.CodeID != "" {
		q.Set("code_id", req.CodeID)
	}

	httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if reqErr != nil {
		return nil, fmt.Errorf("building sentry request: %w", reqErr)
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.token)
	httpReq.Header.Set("Accept", "application/json")

	resp, doErr := s.client.Do(httpReq)
	if doErr != nil {
		return nil, fmt.Errorf("querying sentry: %w", doErr)
	}

	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("sentry api: %s", resp.Status)
	}

	var files []debugFile
	if decErr := json.NewDecoder(resp.Body).Decode(&files); decErr != nil {
		return nil, fmt.Errorf("decoding sentry response: %w", decErr)
	}

	best := pick(files, req)
	if best == nil || len(best.SHA1) < minSHA1Len {
		return nil, ErrNotFound
	}

	return &Location{
		Bucket: s.bucket,
		Key:    s.storageKey(best.SHA1),
		Size:   best.Size,
	}, nil
}

// pick selects the best debug file for req: entries matching its identity and on
// the right side of the source-bundle / binary divide, ranked by how well they
// satisfy the requested file type. Ranked with slices.SortFunc, never an ad-hoc
// sort.
func pick(files []debugFile, req Request) *debugFile {
	wantSource := req.FileType == FileSourceBundle

	matches := make([]debugFile, 0, len(files))
	for _, f := range files {
		if !matchesIdentity(f, req) {
			continue
		}
		// Never serve a source bundle as a binary or vice versa: they decode differently.
		if isSourceBundle(f) != wantSource {
			continue
		}

		matches = append(matches, f)
	}

	if len(matches) == 0 {
		return nil
	}

	slices.SortFunc(matches, func(a, b debugFile) int {
		if d := quality(a, req) - quality(b, req); d != 0 {
			return d
		}
		// SortFunc is not stable; break ties on the content hash so equally-ranked
		// candidates select deterministically across requests and replicas.
		return strings.Compare(a.SHA1, b.SHA1)
	})

	return &matches[0]
}

func matchesIdentity(f debugFile, req Request) bool {
	if req.DebugID != "" && strings.EqualFold(f.DebugID, req.DebugID) {
		return true
	}

	return req.CodeID != "" && f.CodeID != "" && strings.EqualFold(f.CodeID, req.CodeID)
}

func isSourceBundle(f debugFile) bool {
	return strings.EqualFold(f.SymbolType, symbolTypeSourceBundle)
}

// quality is a lower-is-better rank within an identity's candidates: prefer the
// entry that carries the requested DIF feature (debug vs unwind) and, when we
// know a real module name, an exact filename match.
func quality(f debugFile, req Request) int {
	score := 2
	if feat := wantedFeature(req.FileType); feat == "" || slices.ContainsFunc(f.Data.Features, func(s string) bool {
		return strings.EqualFold(s, feat)
	}) {
		score = 1
	}

	if req.Filename != "" && strings.EqualFold(f.ObjectName, req.Filename) {
		score--
	}

	return score
}

// wantedFeature maps a file type to the Sentry DIF feature that distinguishes it
// from its sibling (debug info vs unwind/code). Source bundles need no feature.
func wantedFeature(ft FileType) string {
	switch ft {
	case FilePDB, FileELFDebug, FileMachDebug:
		return "debug"
	case FilePE, FileELFCode, FileMachCode:
		return "unwind"
	default:
		return ""
	}
}

func (s *Sentry) storageKey(sha1 string) string {
	var b strings.Builder
	b.Grow(len(s.keyPrefix) + len(sha1) + 6)

	if s.keyPrefix != "" {
		b.WriteString(s.keyPrefix)

		if !strings.HasSuffix(s.keyPrefix, "/") {
			b.WriteByte('/')
		}
	}

	b.WriteString(sha1[0:2])
	b.WriteByte('/')
	b.WriteString(sha1[2:4])
	b.WriteByte('/')
	b.WriteString(sha1)

	return b.String()
}
