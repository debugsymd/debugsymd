package objects

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/debugsymd/debugsymd/resolver"
)

// buildSourceBundle returns a minimal Sentry-style source bundle: a manifest
// mapping a zip entry to an original source path, plus that entry's bytes.
func buildSourceBundle(t *testing.T, sourcePath string, content []byte) []byte {
	t.Helper()

	manifest := bundleManifest{Files: map[string]struct {
		Path string `json:"path"`
	}{
		"files/0": {Path: sourcePath},
	}}

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	mw, err := zw.Create(bundleManifestName)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.NewEncoder(mw).Encode(manifest); err != nil {
		t.Fatal(err)
	}

	fw, err := zw.Create("files/0")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestSourceFile(t *testing.T) {
	content := []byte("int main(void) { return 0; }\n")
	bundle := buildSourceBundle(t, "/usr/src/app/main.c", content)

	svc := newService(t, &fakeFetcher{payload: bundle})
	req := resolver.Request{CodeID: "GOOD", Filename: "_.src.zip", FileType: resolver.FileSourceBundle}

	// Requested with a differently-spelled (relative, leading-slash-less) path —
	// normalization must still match the manifest's absolute path.
	got, err := svc.SourceFile(context.Background(), req, "usr/src/app/main.c")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, content) {
		t.Fatalf("source = %q, want %q", got, content)
	}

	if _, err := svc.SourceFile(context.Background(), req, "/nope.c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing path err = %v, want ErrNotFound", err)
	}
}

func TestSourceFileTooLarge(t *testing.T) {
	// An entry past the cap must be rejected, not read whole into memory. Zeros
	// keep the zip small on disk while still decompressing past the ceiling.
	oversized := make([]byte, maxSourceFileSize+1)
	bundle := buildSourceBundle(t, "/usr/src/app/big.c", oversized)

	svc := newService(t, &fakeFetcher{payload: bundle})
	req := resolver.Request{CodeID: "GOOD", Filename: "_.src.zip", FileType: resolver.FileSourceBundle}

	if _, err := svc.SourceFile(context.Background(), req, "usr/src/app/big.c"); err == nil {
		t.Fatal("oversized source entry: want error, got nil")
	}
}
