package diskcache

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
)

// key derives the content-addressing hash for a cache entry. The role and inner
// filename are part of the hash because the same underlying object is stored
// under several roles (objects / raw_compressed / cab_synth) and the CAB
// envelope embeds the inner filename, so a different filename must map to a
// different entry. The hash is fed incrementally to avoid building a joined
// string.
func key(role, debugID, inner string) string {
	h := sha256.New()
	// hash.Hash.Write never returns an error.
	_, _ = io.WriteString(h, debugID)
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, role)
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, inner)

	return hex.EncodeToString(h.Sum(nil))
}

// path builds the absolute on-disk location for an entry, sharded two levels
// deep by the first two hash byte-pairs (<root>/<role>/<hh>/<hh>/<sha256>) to
// keep directory fan-out bounded. Built with a single pre-sized strings.Builder
// rather than a chain of joins.
func (c *Cache) path(role, debugID, inner string) string {
	sum := key(role, debugID, inner)
	sep := filepath.Separator

	var b strings.Builder
	b.Grow(len(c.root) + len(role) + len(sum) + 6)
	b.WriteString(c.root)
	b.WriteRune(sep)
	b.WriteString(role)
	b.WriteRune(sep)
	b.WriteString(sum[0:2])
	b.WriteRune(sep)
	b.WriteString(sum[2:4])
	b.WriteRune(sep)
	b.WriteString(sum)

	return b.String()
}
