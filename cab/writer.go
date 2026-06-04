// Package cab synthesizes single-folder, single-file Microsoft Cabinet (CAB)
// archives using MSZIP compression. It exists because no standard-library or
// production-quality Go CAB writer does, and Windows debuggers require the
// compressed symbol forms (.pd_/.dl_/.ex_) to be genuine CAB envelopes.
//
// The writer streams its input: it never buffers the whole object in memory,
// so it is safe for the multi-GB PDBs debugsymd serves (subject to the CAB
// format's own per-folder size ceiling — see Write).
package cab

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrFileTooLarge is returned when the input exceeds what a single-folder CAB
// can represent. A CFFOLDER addresses at most 0xFFFF CFDATA blocks of 32 KiB
// each (~2 GiB uncompressed); larger inputs would require a multi-folder
// cabinet, which we do not synthesize. In practice debuggers fetch oversized
// PDBs uncompressed (.pdb) rather than as .pd_, so this ceiling is not a
// problem for the realistic request mix.
var ErrFileTooLarge = errors.New("cab: input exceeds the single-folder CAB size limit")

// ErrSizeMismatch is returned when the reader passed to Write does not yield
// exactly the declared uncompressedSize. Synthesizing the cabinet anyway would
// emit a CFFILE.cbFile that disagrees with the streamed CFDATA, which decoders
// reject or silently truncate.
var ErrSizeMismatch = errors.New("cab: input length does not match declared size")

// Write wraps r (exactly uncompressedSize bytes) in a single-folder,
// single-file MSZIP cabinet written to out. innerFilename is the name a tool
// such as expand.exe or WinDbg sees when extracting — it must be the
// uncompressed form (e.g. "foo.pdb", never "foo.pd_").
//
// out must be seekable: the cabinet header records the total size and the
// folder records the block count, neither of which is known until the body has
// been streamed, so both are back-patched at the end.
func Write(out io.WriteSeeker, innerFilename string, uncompressedSize int64, r io.Reader) error {
	if uncompressedSize < 0 || uncompressedSize > maxFolderUncompessed {
		return ErrFileTooLarge
	}

	name, err := encodeName(innerFilename)
	if err != nil {
		return err
	}

	cffileSize := cfFileFixedSize + len(name)
	coffFiles := cfHeaderSize + cfFolderSize
	firstDataOffset := coffFiles + cffileSize

	w := &errWriter{w: out}
	writeHeader(w, coffFiles)
	writeFolder(w, firstDataOffset)
	writeFile(w, uncompressedSize, name)

	if w.err != nil {
		return w.err
	}

	blocks, total, streamErr := streamBody(w, r, int64(firstDataOffset), uncompressedSize)
	if streamErr != nil {
		return streamErr
	}

	return patch(out, total, blocks)
}

// encodeName validates and NUL-terminates a CAB file name. CAB names are byte
// strings; we restrict to printable ASCII (the symbol filenames we handle) so
// we never need the UTF-8 name attribute.
func encodeName(name string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("cab: empty file name")
	}

	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] > 0x7e {
			return nil, fmt.Errorf("cab: file name %q has a non-printable-ASCII byte", name)
		}
	}

	out := make([]byte, len(name)+1)
	copy(out, name)

	return out, nil
}

func writeHeader(w *errWriter, coffFiles int) {
	var h [cfHeaderSize]byte
	copy(h[0:4], headerSignature)
	// reserved1 (4), cbCabinet (8, patched), reserved2 (12) stay zero.
	// #nosec G115 -- coffFiles is the fixed header+folder size (44 bytes).
	binary.LittleEndian.PutUint32(h[16:], uint32(coffFiles))
	// reserved3 (20) stays zero.
	h[24] = versionMinor
	h[25] = versionMajor
	binary.LittleEndian.PutUint16(h[26:], 1) // cFolders
	binary.LittleEndian.PutUint16(h[28:], 1) // cFiles
	// flags (30), setID (32), iCabinet (34) stay zero.
	w.write(h[:])
}

func writeFolder(w *errWriter, firstDataOffset int) {
	var f [cfFolderSize]byte
	// #nosec G115 -- firstDataOffset is header+folder+file, a few KB bounded by the name length.
	binary.LittleEndian.PutUint32(f[0:], uint32(firstDataOffset)) // coffCabStart
	// cCFData (offset 4) is patched after streaming.
	binary.LittleEndian.PutUint16(f[6:], compressionMSZIP)
	w.write(f[:])
}

func writeFile(w *errWriter, size int64, name []byte) {
	f := make([]byte, cfFileFixedSize+len(name))
	// #nosec G115 -- size is guarded ≤ maxFolderUncompessed (~2 GiB) at Write's entry.
	binary.LittleEndian.PutUint32(f[0:], uint32(size)) // cbFile
	// uoffFolderStart (4) and iFolder (8) stay zero: single file in folder 0.
	binary.LittleEndian.PutUint16(f[10:], dosDate)
	binary.LittleEndian.PutUint16(f[12:], dosTime)
	binary.LittleEndian.PutUint16(f[14:], attribArchive)
	copy(f[16:], name)
	w.write(f)
}

// streamBody reads r in 32 KiB chunks, MSZIP-compresses each into a CFDATA
// block, and returns the block count and the final cabinet size. It always
// emits at least one block (an empty block for empty input) so the folder is
// well-formed. It verifies r yields exactly uncompressedSize bytes — the value
// already written into the CFFILE cbFile field — so a short or over-long reader
// can never produce a cabinet whose header disagrees with its contents.
func streamBody(w *errWriter, r io.Reader, firstDataOffset, uncompressedSize int64) (uint16, int64, error) {
	mw := newMszipWriter()
	buf := make([]byte, maxBlockUncompressed)

	var hdr [cfDataHeaderLen]byte

	var blocks uint16

	total := firstDataOffset

	var seen int64

	for {
		n, eof, err := readChunk(r, buf)
		if err != nil {
			return 0, 0, err
		}

		seen += int64(n)

		if n > 0 || blocks == 0 {
			payload, blockErr := mw.block(buf[:n])
			if blockErr != nil {
				return 0, 0, blockErr
			}

			if blocks == maxBlocks {
				return 0, 0, ErrFileTooLarge
			}

			cbData := len(payload)

			binary.LittleEndian.PutUint32(hdr[0:], 0) // csum: not computed
			// #nosec G115 -- a block payload is "CK"+≤32 KiB of (de)compressed data, < MaxUint16.
			binary.LittleEndian.PutUint16(hdr[4:], uint16(cbData))
			// #nosec G115 -- n is the chunk length, ≤ maxBlockUncompressed (32 KiB) ≤ MaxUint16.
			binary.LittleEndian.PutUint16(hdr[6:], uint16(n)) // cbUncomp
			w.write(hdr[:])
			w.write(payload)

			if w.err != nil {
				return 0, 0, w.err
			}

			blocks++
			total += int64(cfDataHeaderLen + cbData)
		}

		if eof {
			break
		}
	}

	if seen != uncompressedSize {
		return 0, 0, fmt.Errorf("%w: read %d bytes, declared %d", ErrSizeMismatch, seen, uncompressedSize)
	}

	return blocks, total, nil
}

// readChunk fills buf via ReadFull, reporting how many bytes were read and
// whether the input ended.
func readChunk(r io.Reader, buf []byte) (int, bool, error) {
	n, err := io.ReadFull(r, buf)
	switch {
	case err == nil:
		return n, false, nil
	case errors.Is(err, io.EOF):
		return 0, true, nil
	case errors.Is(err, io.ErrUnexpectedEOF):
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("reading input: %w", err)
	}
}

// patch back-fills the two fields only known after the body is written.
func patch(out io.WriteSeeker, total int64, blocks uint16) error {
	var b4 [4]byte
	// #nosec G115 -- total ≤ maxFolderUncompessed (~2 GiB) plus per-block overhead, < MaxUint32.
	binary.LittleEndian.PutUint32(b4[:], uint32(total))

	if _, err := out.Seek(offCbCabinet, io.SeekStart); err != nil {
		return fmt.Errorf("seeking to cbCabinet field: %w", err)
	}

	if _, err := out.Write(b4[:]); err != nil {
		return fmt.Errorf("patching cbCabinet: %w", err)
	}

	var b2 [2]byte
	binary.LittleEndian.PutUint16(b2[:], blocks)

	if _, err := out.Seek(offCCFData, io.SeekStart); err != nil {
		return fmt.Errorf("seeking to cCFData field: %w", err)
	}

	if _, err := out.Write(b2[:]); err != nil {
		return fmt.Errorf("patching cCFData: %w", err)
	}

	// Leave the position at end so a following fsync/stat sees the full cabinet.
	if _, err := out.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seeking to end: %w", err)
	}

	return nil
}

// errWriter collapses the repeated write-error checks of a fixed byte layout
// into a single deferred check, so the layout code reads as a sequence of
// writes rather than error plumbing.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) write(p []byte) {
	if e.err != nil {
		return
	}

	_, e.err = e.w.Write(p)
}
