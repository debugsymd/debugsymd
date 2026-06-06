package httpapi

import (
	"net/http"

	"github.com/debugsymd/debugsymd/objects"
	"github.com/debugsymd/debugsymd/resolver"
	"github.com/debugsymd/debugsymd/symstore"
)

// debuginfod path constants. The protocol keys everything on the GNU build-id:
//
//	GET /buildid/<hex>/debuginfo        -> the separate ELF debug file
//	GET /buildid/<hex>/executable       -> the ELF binary
//	GET /buildid/<hex>/source/<path...> -> a source file, by absolute path
const (
	placeholderDebug      = "_.debug"
	placeholderExecutable = "_"
	placeholderBundle     = "_.src.zip"
)

// debuginfodDebug serves the ELF debug file for a build-id.
func (h *Handler) debuginfodDebug(w http.ResponseWriter, r *http.Request) {
	h.serveELF(w, r, resolver.FileELFDebug, placeholderDebug)
}

// debuginfodExecutable serves the ELF executable/library for a build-id.
func (h *Handler) debuginfodExecutable(w http.ResponseWriter, r *http.Request) {
	h.serveELF(w, r, resolver.FileELFCode, placeholderExecutable)
}

func (h *Handler) serveELF(w http.ResponseWriter, r *http.Request, ft resolver.FileType, placeholder string) {
	req, ok := symstore.ELFRequest(r.PathValue("buildid"), placeholder, ft)
	if !ok {
		http.NotFound(w, r)
		return
	}

	file, info, err := h.objects.Fetch(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if file == nil {
		h.fail(w, r, objects.ErrNotFound)
		return
	}

	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", symstore.OctetStream)
	// w is the statusRecorder from withMetrics (see symstoreRoute).
	http.ServeContent(w, r, req.Filename, info.ModTime(), file)
}

// debuginfodSource serves a single source file out of the build-id's source
// bundle. The path after `source/` is the file's absolute path.
func (h *Handler) debuginfodSource(w http.ResponseWriter, r *http.Request) {
	srcPath := r.PathValue("srcpath")
	if srcPath == "" {
		http.NotFound(w, r)
		return
	}

	req, ok := symstore.ELFRequest(r.PathValue("buildid"), placeholderBundle, resolver.FileSourceBundle)
	if !ok {
		http.NotFound(w, r)
		return
	}

	data, err := h.objects.SourceFile(r.Context(), req, srcPath)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	w.Header().Set("Content-Type", symstore.OctetStream)
	// #nosec G705 -- served as application/octet-stream (set above), not HTML,
	// so it cannot execute as script in a browser.
	_, _ = w.Write(data)
}
