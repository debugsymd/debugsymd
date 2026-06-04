package symstore

// Content types for the three ways debugsymd serves a symbol payload.
const (
	OctetStream    = "application/octet-stream"          // raw object bytes
	CABContentType = "application/vnd.ms-cab-compressed" // PE/PDB compressed forms
	ZipContentType = "application/zip"                   // source bundles
)

// Signature prefixes from the symbolicator/SSQP layout. Longer (sym) prefixes
// must be tested before their shorter code prefixes.
const (
	prefixELFDebug  = "elf-buildid-sym-"
	prefixELFCode   = "elf-buildid-"
	prefixMachDebug = "mach-uuid-sym-"
	prefixMachCode  = "mach-uuid-"
)

const (
	extPDB             = ".pdb"
	suffixSourceBundle = ".src.zip"

	// uuidHexLen is a UUID/PDB GUID as hex without dashes; uuidByteLen is the same
	// in raw bytes (the ELF build-id prefix used for its debug ID).
	uuidHexLen  = 32
	uuidByteLen = 16
)

// compressedExtensions maps each Microsoft compressed extension to the final
// character of its uncompressed form (the trailing `_` is replaced):
// pd_->pdb, dl_->dll, ex_->exe.
var compressedExtensions = []struct {
	suffix      string
	replacement byte
}{
	{"pd_", 'b'},
	{"dl_", 'l'},
	{"ex_", 'e'},
}
