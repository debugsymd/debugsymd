// Package symstore parses Microsoft symsrv / SSQP symbol-server paths into a
// resolver request plus a serving decision, re-implementing Sentry symbolicator's
// path grammar (PE, PDB, ELF, Mach-O, source bundles). It is the single home of
// the path conventions and identifier normalization; both the symstore and
// debuginfod HTTP surfaces build their requests through it.
package symstore

import (
	"strings"

	"github.com/debugsymd/debugsymd/resolver"
)

// Serve describes how the matched object should be returned to the client.
type Serve struct {
	ContentType string
	// CAB marks the Microsoft compressed forms (.pd_/.dl_/.ex_), which must be
	// wrapped in a CAB envelope (objects.FetchCompressed) rather than served verbatim.
	CAB bool
}

// Parse interprets a symstore path's three segments (leading/trailing filenames,
// middle signature), returning the resolver request and serving decision, or
// ok=false for an unrecognized signature. Callers must already have rejected
// leading/trailing mismatches.
func Parse(leading, signature, trailing string) (resolver.Request, Serve, bool) {
	// Microsoft compressed leaf (PE/PDB) -> CAB envelope.
	if inner, ok := uncompressLeaf(trailing); ok {
		req, parsed := baseIdentity(signature, inner, strings.HasSuffix(strings.ToLower(inner), extPDB))
		if !parsed {
			return resolver.Request{}, Serve{}, false
		}

		return req, Serve{ContentType: CABContentType, CAB: true}, true
	}

	// Source bundle -> zip, served verbatim. Non-prefixed signatures are PDB debug
	// IDs (bundles derive from debug files); ELF/Mach are detected by signature prefix.
	if isSourceBundle(trailing) {
		req, parsed := baseIdentity(signature, trailing, true)
		if !parsed {
			return resolver.Request{}, Serve{}, false
		}

		req.FileType = resolver.FileSourceBundle

		return req, Serve{ContentType: ZipContentType}, true
	}

	// Plain object -> octet-stream, served verbatim.
	req, parsed := baseIdentity(signature, trailing, strings.HasSuffix(strings.ToLower(leading), extPDB))
	if !parsed {
		return resolver.Request{}, Serve{}, false
	}

	return req, Serve{ContentType: OctetStream}, true
}

// baseIdentity maps a signature (plus the resolved filename and whether the
// leading name looked like a .pdb) to a resolver request. pdbHint disambiguates
// the non-prefixed PE-vs-PDB case.
func baseIdentity(signature, filename string, pdbHint bool) (resolver.Request, bool) {
	switch {
	case strings.HasPrefix(signature, prefixELFDebug):
		return elfRequest(signature[len(prefixELFDebug):], filename, resolver.FileELFDebug)
	case strings.HasPrefix(signature, prefixELFCode):
		return elfRequest(signature[len(prefixELFCode):], filename, resolver.FileELFCode)
	case strings.HasPrefix(signature, prefixMachDebug):
		return machRequest(signature[len(prefixMachDebug):], filename, resolver.FileMachDebug)
	case strings.HasPrefix(signature, prefixMachCode):
		return machRequest(signature[len(prefixMachCode):], filename, resolver.FileMachCode)
	case pdbHint:
		debugID, ok := normalizePDB(signature)

		return resolver.Request{DebugID: debugID, Filename: filename, FileType: resolver.FilePDB}, ok
	default:
		if signature == "" {
			return resolver.Request{}, false
		}

		return resolver.Request{CodeID: signature, Filename: filename, FileType: resolver.FilePE}, true
	}
}

// ELFRequest builds a resolver request for a raw GNU build-id, as used by the
// debuginfod protocol (which keys everything on the build-id). filename is a
// synthetic placeholder used for the cache key and the served name.
func ELFRequest(buildID, filename string, ft resolver.FileType) (resolver.Request, bool) {
	return elfRequest(buildID, filename, ft)
}

func elfRequest(buildID, filename string, ft resolver.FileType) (resolver.Request, bool) {
	if !isHex(buildID) {
		return resolver.Request{}, false
	}

	req := resolver.Request{CodeID: strings.ToLower(buildID), Filename: filename, FileType: ft}
	if debugID, ok := elfDebugID(buildID); ok {
		req.DebugID = debugID
	}

	return req, true
}

func machRequest(uuid, filename string, ft resolver.FileType) (resolver.Request, bool) {
	simple, dashed, ok := normalizeUUID(uuid)
	if !ok {
		return resolver.Request{}, false
	}

	return resolver.Request{CodeID: simple, DebugID: dashed, Filename: filename, FileType: ft}, true
}

func isSourceBundle(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), suffixSourceBundle)
}

// uncompressLeaf reports whether leaf is a Microsoft compressed filename
// (`<name>.{pd_,dl_,ex_}`, case-insensitive) and, if so, returns its uncompressed
// form with the trailing `_` replaced by b/l/e. The replacement's case follows
// Microsoft's convention: uppercase only when every alphabetic char of the
// extension is uppercase (`EX_`->`EXE`); otherwise lowercase (`Cmd.Ex_`->`Cmd.Exe`).
func uncompressLeaf(leaf string) (string, bool) {
	dot := strings.LastIndexByte(leaf, '.')
	if dot < 0 {
		return "", false
	}

	ext := leaf[dot+1:]

	var replacement byte

	matched := false

	for _, c := range compressedExtensions {
		if strings.EqualFold(ext, c.suffix) {
			replacement = c.replacement
			matched = true

			break
		}
	}

	if !matched {
		return "", false
	}

	if allUpper(strings.TrimRight(ext, "_")) {
		replacement -= 'a' - 'A'
	}

	var b strings.Builder
	b.Grow(len(leaf))
	b.WriteString(leaf[:len(leaf)-1])
	b.WriteByte(replacement)

	return b.String(), true
}

// allUpper reports whether s is non-empty and every ASCII letter in it is
// uppercase (non-letters ignored).
func allUpper(s string) bool {
	if s == "" {
		return false
	}

	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return false
		}
	}

	return true
}
