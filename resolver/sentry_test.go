package resolver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSentryLookupPDB(t *testing.T) {
	const body = `[
		{"debugId":"0c1033f7-8632-492e-91c6-c314b72e1920","objectName":"other.pdb","symbolType":"pdb","sha1":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef","size":10,"data":{"features":["debug"]}},
		{"debugId":"0C1033F7-8632-492E-91C6-C314B72E1920","objectName":"integration.pdb","symbolType":"pdb","sha1":"abcd1234abcd1234abcd1234abcd1234abcd1234","size":4096,"data":{"features":["debug"]}}
	]`

	var gotAuth, gotDebugID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotDebugID = r.URL.Query().Get("debug_id")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewSentry(SentryOptions{
		APIURL:    srv.URL + "/api/0",
		Org:       "corp",
		Project:   "client",
		Token:     "secret",
		Bucket:    "dsyms-prod",
		KeyPrefix: "store",
	})

	loc, err := s.Lookup(context.Background(), Request{
		DebugID:  "0C1033F7-8632-492E-91C6-C314B72E1920",
		Filename: "integration.pdb",
		FileType: FilePDB,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", gotAuth)
	}

	if gotDebugID != "0C1033F7-8632-492E-91C6-C314B72E1920" {
		t.Fatalf("debug_id query = %q", gotDebugID)
	}
	// Filename match should win over the other entry for the same debug ID.
	if loc.Key != "store/ab/cd/abcd1234abcd1234abcd1234abcd1234abcd1234" {
		t.Fatalf("key = %q", loc.Key)
	}

	if loc.Size != 4096 {
		t.Fatalf("size = %d, want 4096", loc.Size)
	}
}

func TestSentryLookupByCodeID(t *testing.T) {
	const (
		buildID = "b5381a457906d279ffffffffffffffffffffffff"
		body    = `[
		{"codeId":"b5381a457906d279ffffffffffffffffffffffff","objectName":"_.debug","symbolType":"elf","sha1":"11112222111122221111222211112222aaaabbbb","size":2048,"data":{"features":["debug","unwind","symtab"]}}
	]`
	)

	var gotCodeID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCodeID = r.URL.Query().Get("code_id")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewSentry(SentryOptions{APIURL: srv.URL, Bucket: "b"})

	loc, err := s.Lookup(context.Background(), Request{
		CodeID:   buildID,
		Filename: "_.debug",
		FileType: FileELFDebug,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotCodeID != buildID {
		t.Fatalf("code_id query = %q, want %q", gotCodeID, buildID)
	}

	if loc.Size != 2048 {
		t.Fatalf("size = %d, want 2048", loc.Size)
	}
}

// A source bundle and an ELF debug file can share an identity; each must be
// selected for its own request and never confused with the other.
func TestSentryLookupSourceBundleVsBinary(t *testing.T) {
	const body = `[
		{"codeId":"abcd","objectName":"libfoo.so","symbolType":"elf","sha1":"1111111111111111111111111111111111111111","size":10,"data":{"features":["debug"]}},
		{"codeId":"abcd","objectName":"libfoo.so","symbolType":"sourcebundle","sha1":"2222222222222222222222222222222222222222","size":20,"data":{"features":[]}}
	]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewSentry(SentryOptions{APIURL: srv.URL, Bucket: "b"})

	bundle, err := s.Lookup(context.Background(), Request{CodeID: "abcd", FileType: FileSourceBundle})
	if err != nil {
		t.Fatal(err)
	}

	if bundle.Size != 20 {
		t.Fatalf("source bundle size = %d, want 20 (must not pick the ELF)", bundle.Size)
	}

	binary, err := s.Lookup(context.Background(), Request{CodeID: "abcd", FileType: FileELFDebug})
	if err != nil {
		t.Fatal(err)
	}

	if binary.Size != 10 {
		t.Fatalf("elf size = %d, want 10 (must not pick the source bundle)", binary.Size)
	}
}

func TestSentryLookupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	s := NewSentry(SentryOptions{APIURL: srv.URL, Bucket: "b"})
	if _, err := s.Lookup(context.Background(), Request{DebugID: "MISSING", FileType: FilePDB}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSentryLookup404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := NewSentry(SentryOptions{APIURL: srv.URL, Bucket: "b"})
	if _, err := s.Lookup(context.Background(), Request{DebugID: "X", FileType: FilePDB}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
