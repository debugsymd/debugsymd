package objects

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/debugsymd/debugsymd/resolver"
)

// bundleManifest is the subset of a Sentry/symbolic source-bundle manifest we
// read: a map from the bundle's zip entry name to that file's original source
// path. (Shape is a deployment assumption — see the plan's open items.)
type bundleManifest struct {
	Files map[string]struct {
		Path string `json:"path"`
	} `json:"files"`
}

const bundleManifestName = "manifest.json"

// SourceFile fetches (and caches) the source bundle described by req — which
// must be a FileSourceBundle request — and returns the contents of the source
// file whose manifest path matches sourcePath. Source files are small text
// files, so the matched entry is read fully into memory. Returns ErrNotFound if
// the bundle or the requested path is absent.
func (s *Service) SourceFile(ctx context.Context, req resolver.Request, sourcePath string) ([]byte, error) {
	file, info, err := s.object(ctx, req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	zr, zipErr := zip.NewReader(file, info.Size())
	if zipErr != nil {
		return nil, fmt.Errorf("opening source bundle: %w", zipErr)
	}

	entry, entryErr := bundleEntry(zr, sourcePath)
	if entryErr != nil {
		return nil, entryErr
	}

	rc, openErr := entry.Open()
	if openErr != nil {
		return nil, fmt.Errorf("opening source entry: %w", openErr)
	}

	defer func() { _ = rc.Close() }()

	// Source files are small text files; cap the read so a malformed or
	// maliciously-inflated bundle entry (a zip whose declared size lies) cannot
	// exhaust memory. Read one byte past the ceiling to detect an overrun.
	data, readErr := io.ReadAll(io.LimitReader(rc, maxSourceFileSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("reading source entry: %w", readErr)
	}

	if int64(len(data)) > maxSourceFileSize {
		return nil, fmt.Errorf("source entry %q exceeds the %d-byte limit", sourcePath, maxSourceFileSize)
	}

	return data, nil
}

// bundleEntry locates the zip entry whose manifest path matches sourcePath.
func bundleEntry(zr *zip.Reader, sourcePath string) (*zip.File, error) {
	byName := make(map[string]*zip.File, len(zr.File))

	var manifestFile *zip.File

	for _, f := range zr.File {
		byName[f.Name] = f

		if f.Name == bundleManifestName {
			manifestFile = f
		}
	}

	if manifestFile == nil {
		return nil, ErrNotFound
	}

	manifest, err := readManifest(manifestFile)
	if err != nil {
		return nil, err
	}

	want := normalizeSourcePath(sourcePath)
	for entryName, meta := range manifest.Files {
		if normalizeSourcePath(meta.Path) != want {
			continue
		}

		if f := byName[entryName]; f != nil {
			return f, nil
		}
	}

	return nil, ErrNotFound
}

func readManifest(f *zip.File) (bundleManifest, error) {
	rc, err := f.Open()
	if err != nil {
		return bundleManifest{}, fmt.Errorf("opening source-bundle manifest: %w", err)
	}

	defer func() { _ = rc.Close() }()

	var manifest bundleManifest
	if decErr := json.NewDecoder(rc).Decode(&manifest); decErr != nil {
		return bundleManifest{}, fmt.Errorf("decoding source-bundle manifest: %w", decErr)
	}

	return manifest, nil
}

// normalizeSourcePath canonicalizes a source path for comparison: a cleaned,
// absolute, forward-slash path. debuginfod source requests and manifest paths
// can differ in leading slashes and `.`/`..` segments.
func normalizeSourcePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return path.Clean(p)
}
