package cab

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
)

// mszipWriter produces the CFDATA payloads (each prefixed with "CK") for an
// MSZIP folder.
//
// Each block is an independent, self-contained deflate stream (no cross-block
// back-references). MSZIP decompressors reset the window position at every "CK"
// block, so a block must never reference a previous block; compressing each
// chunk with a fresh deflate history (flate.Writer.Reset) guarantees that. This
// is the simplest correct encoding and round-trips through zlib-quality
// decoders (libarchive, Windows cabinet.dll). Only one 32 KiB chunk is buffered
// at a time, so a multi-GB input never lands in memory.
//
// Note: cabextract/libmspack ≤1.11 has an inflate bug that rejects valid Go
// deflate streams containing long matches (highly compressible input); it is
// not a suitable conformance oracle. The tests validate against libarchive.
type mszipWriter struct {
	sink *bytes.Buffer // raw flate output for the current block
	fw   *flate.Writer // reused across blocks; Reset gives each an empty history
	out  *bytes.Buffer // assembled CFDATA payload for the current block
}

func newMszipWriter() *mszipWriter {
	sink := &bytes.Buffer{}
	fw, _ := flate.NewWriter(sink, flate.DefaultCompression)

	return &mszipWriter{sink: sink, fw: fw, out: &bytes.Buffer{}}
}

// block compresses one ≤32 KiB chunk and returns its CFDATA payload (the "CK"
// signature followed by the block's deflate data). The returned slice aliases
// an internal buffer and is valid only until the next call.
//
// If deflating would not shrink the data, the block is emitted as a deflate
// stored block instead, bounding the output so it can never expand.
func (m *mszipWriter) block(data []byte) ([]byte, error) {
	m.sink.Reset()
	m.fw.Reset(m.sink) // empty history: this block references nothing prior

	if _, err := m.fw.Write(data); err != nil {
		return nil, fmt.Errorf("deflating block: %w", err)
	}

	if err := m.fw.Close(); err != nil {
		return nil, fmt.Errorf("finishing deflate block: %w", err)
	}

	deflated := m.sink.Bytes()

	m.out.Reset()
	m.out.Write(mszipSignature[:])

	if len(deflated) > storedHeaderLen+len(data) {
		m.writeStored(data)
	} else {
		m.out.Write(deflated)
	}

	return m.out.Bytes(), nil
}

// writeStored appends a deflate "stored" (uncompressed) block carrying data
// verbatim — a single BFINAL block — to m.out (after the "CK" already written).
func (m *mszipWriter) writeStored(data []byte) {
	m.out.WriteByte(deflateStoredFinalByte)

	var lens [4]byte
	// #nosec G115 -- data is one block, len(data) ≤ maxBlockUncompressed (32 KiB) ≤ MaxUint16.
	binary.LittleEndian.PutUint16(lens[0:], uint16(len(data)))
	// #nosec G115 -- same bound; the complement is still a 16-bit value.
	binary.LittleEndian.PutUint16(lens[2:], ^uint16(len(data)))
	m.out.Write(lens[:])
	m.out.Write(data)
}
