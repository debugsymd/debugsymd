package httpapi

import "strings"

// knownForms bounds the cardinality of the metric label produced by form. The
// label is derived from the request's (attacker-controlled) trailing path
// segment, so without an allowlist the expvar map keyed on it would grow without
// bound — a memory-exhaustion vector on the public listener. Only the symbol
// forms debugsymd actually serves get their own label; everything else collapses
// to "other".
// formOther is the catch-all metric label for any request form not in knownForms.
const formOther = "other"

var knownForms = map[string]struct{}{
	"pdb": {}, "pd_": {}, // PDB, uncompressed + Microsoft-compressed
	"dll": {}, "dl_": {}, // PE library
	"exe": {}, "ex_": {}, // PE executable
	"zip": {}, // source bundle (.src.zip)
}

// form returns a bounded-cardinality label for the request's symbol form, used
// for metrics: the lowercased file extension when it is a recognized form, else
// "other".
func form(leaf string) string {
	dot := strings.LastIndexByte(leaf, '.')
	if dot < 0 {
		return formOther
	}

	ext := strings.ToLower(leaf[dot+1:])
	if _, ok := knownForms[ext]; !ok {
		return formOther
	}

	return ext
}
