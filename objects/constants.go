package objects

const (
	// Cache roles for the same object: verbatim bytes, a byte-identical upstream
	// CAB mirror, and on-demand synthesized CAB envelopes.
	roleObjects       = "objects"
	roleRawCompressed = "raw_compressed"
	roleCabSynth      = "cab_synth"

	// cabMagic is the Microsoft Cabinet signature; an upstream object starting
	// with it is preserved rather than re-wrapped.
	cabMagic = "MSCF"

	// maxSourceFileSize caps a single source-bundle entry read into memory. Source
	// files are small text files; the ceiling guards against a malformed or
	// decompression-bomb bundle entry.
	maxSourceFileSize = 64 << 20 // 64 MiB
)
