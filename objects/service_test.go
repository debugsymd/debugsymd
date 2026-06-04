package objects

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/debugsymd/debugsymd/diskcache"
	"github.com/debugsymd/debugsymd/resolver"
)

type fakeResolver struct {
	size int64
}

const goodID = "GOOD"

func (r fakeResolver) Lookup(_ context.Context, req resolver.Request) (*resolver.Location, error) {
	if req.DebugID != goodID && req.CodeID != goodID {
		return nil, resolver.ErrNotFound
	}

	return &resolver.Location{Bucket: "b", Key: "k", Size: r.size}, nil
}

func goodReq(name string) resolver.Request {
	return resolver.Request{DebugID: goodID, Filename: name, FileType: resolver.FilePDB}
}

type fakeFetcher struct {
	payload []byte
	delay   time.Duration
	calls   atomic.Int64
}

func (f *fakeFetcher) Fetch(_ context.Context, _, _ string, w io.Writer) (int64, error) {
	f.calls.Add(1)

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	n, err := w.Write(f.payload)

	return int64(n), err
}

func newService(t *testing.T, f *fakeFetcher) *Service {
	t.Helper()

	c, err := diskcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return New(fakeResolver{size: int64(len(f.payload))}, f, c)
}

func readAllClose(t *testing.T, f *os.File) []byte {
	t.Helper()

	defer func() { _ = f.Close() }()

	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestFetchPassthrough(t *testing.T) {
	payload := []byte("raw pdb bytes")
	f := &fakeFetcher{payload: payload}
	svc := newService(t, f)

	got1 := readAllClose(t, mustFetch(t, svc, "integration.pdb"))
	if !bytes.Equal(got1, payload) {
		t.Fatalf("content = %q, want %q", got1, payload)
	}
	// Second fetch is a cache hit: no additional download.
	got2 := readAllClose(t, mustFetch(t, svc, "integration.pdb"))
	if !bytes.Equal(got2, payload) {
		t.Fatalf("second content mismatch")
	}

	if f.calls.Load() != 1 {
		t.Fatalf("fetcher calls = %d, want 1", f.calls.Load())
	}
}

func TestFetchCompressedSynthesizes(t *testing.T) {
	payload := bytes.Repeat([]byte("symbol data "), 4096)
	f := &fakeFetcher{payload: payload}
	svc := newService(t, f)

	file, _, err := svc.FetchCompressed(context.Background(), goodReq("integration.pdb"))
	if err != nil {
		t.Fatal(err)
	}

	name := file.Name()

	got := readAllClose(t, file)
	if !bytes.HasPrefix(got, []byte("MSCF")) {
		t.Fatalf("expected a CAB envelope, got %x", got[:min(8, len(got))])
	}

	verifyCabExtract(t, name, "integration.pdb", payload)
}

func TestFetchCompressedPassesThroughUpstreamCab(t *testing.T) {
	// Upstream payload is already a cabinet: it must be mirrored byte-for-byte,
	// never re-wrapped.
	payload := append([]byte("MSCF"), bytes.Repeat([]byte{0x01, 0x02}, 64)...)
	f := &fakeFetcher{payload: payload}
	svc := newService(t, f)

	file, _, err := svc.FetchCompressed(context.Background(), goodReq("integration.pdb"))
	if err != nil {
		t.Fatal(err)
	}

	if got := readAllClose(t, file); !bytes.Equal(got, payload) {
		t.Fatalf("upstream CAB not preserved byte-identically")
	}
}

func TestFetchCompressedDedups(t *testing.T) {
	payload := bytes.Repeat([]byte("dedupe me "), 2048)
	f := &fakeFetcher{payload: payload, delay: 40 * time.Millisecond}
	svc := newService(t, f)

	const n = 8

	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			file, _, err := svc.FetchCompressed(context.Background(), goodReq("integration.pdb"))
			if err != nil {
				t.Errorf("concurrent FetchCompressed: %v", err)
				return
			}

			_ = file.Close()
		}()
	}

	wg.Wait()

	if f.calls.Load() != 1 {
		t.Fatalf("fetcher calls = %d, want 1 (singleflight should dedup)", f.calls.Load())
	}
}

func TestNotFound(t *testing.T) {
	f := &fakeFetcher{payload: []byte("x")}
	svc := newService(t, f)

	bad := resolver.Request{DebugID: "BAD", Filename: "x.pdb", FileType: resolver.FilePDB}
	if _, _, err := svc.Fetch(context.Background(), bad); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Fetch err = %v, want ErrNotFound", err)
	}

	if _, _, err := svc.FetchCompressed(context.Background(), bad); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FetchCompressed err = %v, want ErrNotFound", err)
	}
}

func mustFetch(t *testing.T, svc *Service, name string) *os.File {
	t.Helper()

	file, _, err := svc.Fetch(context.Background(), goodReq(name))
	if err != nil {
		t.Fatal(err)
	}

	return file
}

func verifyCabExtract(t *testing.T, cabPath, inner string, want []byte) {
	t.Helper()

	bin, err := exec.LookPath("bsdtar")
	if err != nil {
		t.Logf("bsdtar (libarchive) not installed; skipping content verification")
		return
	}

	dir := t.TempDir()
	// #nosec G204 -- bin is the located bsdtar oracle; cabPath is our fixture.
	cmd := exec.CommandContext(t.Context(), bin, "-xf", cabPath)

	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bsdtar: %v\n%s", err, out)
	}

	// #nosec G304 -- reading a fixture bsdtar just extracted under t.TempDir().
	got, err := os.ReadFile(filepath.Join(dir, inner))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("extracted %d bytes, want %d", len(got), len(want))
	}
}
