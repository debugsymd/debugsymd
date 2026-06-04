package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/debugsymd/debugsymd/objects"
	"github.com/debugsymd/debugsymd/resolver"
	"github.com/debugsymd/debugsymd/symstore"
)

// fakeObjects serves fixed files from disk, or ErrNotFound for an empty path.
// source maps a debuginfod source path to its bytes.
type fakeObjects struct {
	raw    string
	cab    string
	source map[string][]byte
}

func openOr404(path string) (*os.File, fs.FileInfo, error) {
	if path == "" {
		return nil, nil, objects.ErrNotFound
	}

	// #nosec G304 -- test fixture path under t.TempDir(), not attacker-controlled.
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening fixture: %w", err)
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("stat fixture: %w", err)
	}

	return f, fi, nil
}

func (f fakeObjects) Fetch(_ context.Context, _ resolver.Request) (*os.File, fs.FileInfo, error) {
	return openOr404(f.raw)
}

func (f fakeObjects) FetchCompressed(_ context.Context, _ resolver.Request) (*os.File, fs.FileInfo, error) {
	return openOr404(f.cab)
}

func (f fakeObjects) SourceFile(_ context.Context, _ resolver.Request, sourcePath string) ([]byte, error) {
	if b, ok := f.source[sourcePath]; ok {
		return b, nil
	}

	return nil, objects.ErrNotFound
}

func writeFixture(t *testing.T, name string, content []byte) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func newTestServer(t *testing.T, o Objects) *httptest.Server {
	t.Helper()

	h := NewHandler(o)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{leading}/{signature}/{trailing}", h.symstoreRoute)
	mux.HandleFunc("GET /buildid/{buildid}/debuginfo", h.debuginfodDebug)
	mux.HandleFunc("GET /buildid/{buildid}/executable", h.debuginfodExecutable)
	mux.HandleFunc("GET /buildid/{buildid}/source/{srcpath...}", h.debuginfodSource)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// get issues a context-aware GET against the test server. The caller closes the
// returned response body.
func get(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	return resp
}

// assertServed issues a GET and asserts a 200 with the expected content type and
// body.
func assertServed(t *testing.T, srv *httptest.Server, urlPath, wantCT string, want []byte) {
	t.Helper()

	resp := get(t, srv, urlPath)

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != wantCT {
		t.Fatalf("content-type = %q, want %q", ct, wantCT)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, want) {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

const sig = "0C1033F78632492E91C6C314B72E1920ffffffff"

func TestServePassthrough(t *testing.T) {
	raw := []byte("raw pdb contents")
	srv := newTestServer(t, fakeObjects{raw: writeFixture(t, "integration.pdb", raw)})

	assertServed(t, srv, "/integration.pdb/"+sig+"/integration.pdb", symstore.OctetStream, raw)
}

func TestServeELF(t *testing.T) {
	raw := []byte("\x7fELF fake debug info")
	srv := newTestServer(t, fakeObjects{raw: writeFixture(t, "libc.so", raw)})

	assertServed(t, srv, "/_.debug/elf-buildid-sym-"+debuginfodBuildID+"/_.debug", symstore.OctetStream, raw)
}

func TestServeSourceBundle(t *testing.T) {
	zipBytes := []byte("PK\x03\x04 fake source bundle")
	srv := newTestServer(t, fakeObjects{raw: writeFixture(t, "app.src.zip", zipBytes)})

	assertServed(t, srv, "/app.src.zip/"+sig+"/app.src.zip", symstore.ZipContentType, zipBytes)
}

func TestServeCompressed(t *testing.T) {
	cab := []byte("MSCF\x00\x00fake cab envelope")
	srv := newTestServer(t, fakeObjects{cab: writeFixture(t, "integration.cab", cab)})

	assertServed(t, srv, "/integration.pd_/"+sig+"/integration.pd_", symstore.CABContentType, cab)
}

func TestServeHead(t *testing.T) {
	cab := []byte("MSCF\x00\x00fake cab envelope")
	srv := newTestServer(t, fakeObjects{cab: writeFixture(t, "integration.cab", cab)})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, srv.URL+"/integration.pd_/"+sig+"/integration.pd_", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if resp.ContentLength != int64(len(cab)) {
		t.Fatalf("content-length = %d, want %d", resp.ContentLength, len(cab))
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("HEAD body must be empty, got %d bytes", len(body))
	}
}

const debuginfodBuildID = "0102030405060708090a0b0c0d0e0f10aabbccdd"

func TestDebuginfodDebug(t *testing.T) {
	raw := []byte("\x7fELF separate debug info")
	srv := newTestServer(t, fakeObjects{raw: writeFixture(t, "elf.debug", raw)})

	assertServed(t, srv, "/buildid/"+debuginfodBuildID+"/debuginfo", symstore.OctetStream, raw)
}

func TestDebuginfodSource(t *testing.T) {
	content := []byte("int main(void){return 0;}\n")
	srv := newTestServer(t, fakeObjects{source: map[string][]byte{"usr/src/main.c": content}})

	resp := get(t, srv, "/buildid/"+debuginfodBuildID+"/source/usr/src/main.c")

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(content) {
		t.Fatalf("source body = %q, want %q", body, content)
	}

	miss := get(t, srv, "/buildid/"+debuginfodBuildID+"/source/usr/src/absent.c")

	defer func() { _ = miss.Body.Close() }()

	if miss.StatusCode != http.StatusNotFound {
		t.Fatalf("missing source status = %d, want 404", miss.StatusCode)
	}
}

func TestServeErrors(t *testing.T) {
	// fakeObjects{} has empty paths, so every lookup is a miss (ErrNotFound).
	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"filename mismatch", "/foo.pdb/" + sig + "/bar.pdb", http.StatusBadRequest},
		{"not found", "/missing.pdb/" + sig + "/missing.pdb", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, fakeObjects{})
			resp := get(t, srv, tc.path)

			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
}
