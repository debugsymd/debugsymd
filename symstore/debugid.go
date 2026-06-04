package symstore

import (
	"encoding/hex"
	"strings"
)

// normalizePDB converts a symstore PDB signature (`<32-hex GUID><age>`, the
// breakpad form) into a Sentry debug ID: the GUID with dashes inserted, plus the
// age as a lowercase-hex appendix when it is non-zero (matching symbolic's
// DebugId display). The GUID is already in display byte order, so no swap.
func normalizePDB(sig string) (string, bool) {
	if len(sig) < uuidHexLen {
		return "", false
	}

	uuid, ok := dashUUID(sig[:uuidHexLen])
	if !ok {
		return "", false
	}

	age := strings.ToLower(sig[uuidHexLen:])
	if age != "" && !isHex(age) {
		return "", false
	}

	age = strings.TrimLeft(age, "0")
	if age == "" {
		return uuid, true // no age (or all-zero)
	}

	return uuid + "-" + age, true
}

// elfDebugID derives an ELF debug ID from a GNU build-id: the first 16 bytes
// interpreted as a little-endian GUID (symbolic's convention). The full build-id
// remains the code ID.
func elfDebugID(buildID string) (string, bool) {
	b, err := hex.DecodeString(buildID)
	if err != nil || len(b) < uuidByteLen {
		return "", false
	}

	// Swap the first three GUID fields from little-endian to display order;
	// the trailing 8 bytes stay as-is.
	swapped := []byte{
		b[3], b[2], b[1], b[0],
		b[5], b[4],
		b[7], b[6],
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15],
	}

	return dashUUID(hex.EncodeToString(swapped))
}

// normalizeUUID turns a Mach-O UUID (32 hex, with or without dashes; already in
// display order) into its simple (lowercase, no dashes) and dashed forms.
func normalizeUUID(s string) (simple, dashed string, ok bool) {
	s = strings.ToLower(strings.ReplaceAll(s, "-", ""))
	if len(s) != uuidHexLen || !isHex(s) {
		return "", "", false
	}

	dashed, _ = dashUUID(s)

	return s, dashed, true
}

// dashUUID inserts the canonical 8-4-4-4-12 dashes into a 32-hex string.
func dashUUID(h string) (string, bool) {
	if len(h) != uuidHexLen || !isHex(h) {
		return "", false
	}

	h = strings.ToLower(h)

	var b strings.Builder
	b.Grow(len(h) + 4)
	b.WriteString(h[0:8])
	b.WriteByte('-')
	b.WriteString(h[8:12])
	b.WriteByte('-')
	b.WriteString(h[12:16])
	b.WriteByte('-')
	b.WriteString(h[16:20])
	b.WriteByte('-')
	b.WriteString(h[20:])

	return b.String(), true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}

	return true
}
