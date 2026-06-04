package cab

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWriteRoundtrip(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"single block", []byte("hello world, this is a fake PDB")},
		{"empty", nil},
		{"exact block boundary", bytes.Repeat([]byte{0xAB}, maxBlockUncompressed)},
		{"multi block varied", variedPayload(maxBlockUncompressed*3 + 123)},
		{"multi block repetitive", bytes.Repeat([]byte("symbol data "), 8192)},
		{"multi block zeros", make([]byte, maxBlockUncompressed*4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out memFile
			if err := Write(&out, "integration.pdb", int64(len(tc.payload)), bytes.NewReader(tc.payload)); err != nil {
				t.Fatalf("Write: %v", err)
			}

			if !bytes.HasPrefix(out.buf, []byte(headerSignature)) {
				t.Fatalf("missing MSCF magic")
			}

			if got := int(binary.LittleEndian.Uint32(out.buf[offCbCabinet:])); got != len(out.buf) {
				t.Fatalf("cbCabinet = %d, want %d (full file length)", got, len(out.buf))
			}

			name, got := extractCAB(t, out.buf)
			if name != "integration.pdb" {
				t.Fatalf("entry name = %q, want integration.pdb", name)
			}

			if !bytes.Equal(got, tc.payload) {
				t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(tc.payload))
			}
		})
	}
}

func TestWriteRejectsOversizedInput(t *testing.T) {
	// Pass the size without a real reader of that length: the limit is checked
	// up front, so synthesis never starts.
	err := Write(&memFile{}, "huge.pdb", maxFolderUncompessed+1, bytes.NewReader(nil))
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("err = %v, want ErrFileTooLarge", err)
	}
}

func TestWriteRejectsSizeMismatch(t *testing.T) {
	// A reader shorter than the declared size would yield a cabinet whose cbFile
	// overstates its contents.
	if err := Write(&memFile{}, "x.pdb", 100, bytes.NewReader([]byte("short"))); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("short reader err = %v, want ErrSizeMismatch", err)
	}
	// A reader longer than the declared size is equally inconsistent.
	if err := Write(&memFile{}, "x.pdb", 2, bytes.NewReader([]byte("longer"))); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("long reader err = %v, want ErrSizeMismatch", err)
	}
}

func TestWriteRejectsBadName(t *testing.T) {
	if err := Write(&memFile{}, "", 0, bytes.NewReader(nil)); err == nil {
		t.Fatalf("empty name: want error")
	}

	if err := Write(&memFile{}, "foo\x00.pdb", 0, bytes.NewReader(nil)); err == nil {
		t.Fatalf("NUL in name: want error")
	}
}

// TestWriteAgainstLibarchive round-trips through libarchive's `bsdtar` when it
// is installed, validating against an independent, zlib-quality CAB reader (the
// same inflate lineage Windows' cabinet.dll uses). Skipped otherwise.
//
// We deliberately do not use cabextract/libmspack as the oracle: ≤1.11 has an
// inflate bug that rejects valid Go deflate streams with long matches.
func TestWriteAgainstLibarchive(t *testing.T) {
	bin, err := exec.LookPath("bsdtar")
	if err != nil {
		t.Skip("bsdtar (libarchive) not installed")
	}

	payloads := map[string][]byte{
		"varied":     variedPayload(200000),
		"repetitive": bytes.Repeat([]byte("symbol data "), 8192),
		"zeros":      make([]byte, maxBlockUncompressed*3+7),
		"text":       bytes.Repeat([]byte("The quick brown fox jumps. "), 20000),
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cabPath := filepath.Join(dir, "test.cab")

			// #nosec G304 -- cabPath is under t.TempDir().
			f, err := os.Create(cabPath)
			if err != nil {
				t.Fatal(err)
			}

			if err := Write(f, "integration.pdb", int64(len(payload)), bytes.NewReader(payload)); err != nil {
				t.Fatal(err)
			}

			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			// #nosec G204 -- bin is the located bsdtar oracle; cabPath is our fixture.
			cmd := exec.CommandContext(t.Context(), bin, "-xf", cabPath)

			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bsdtar: %v\n%s", err, out)
			}

			// #nosec G304 -- reading a fixture bsdtar just extracted under t.TempDir().
			got, err := os.ReadFile(filepath.Join(dir, "integration.pdb"))
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, payload) {
				t.Fatalf("bsdtar output mismatch: %d vs %d bytes", len(got), len(payload))
			}
		})
	}
}

// variedPayload builds a deterministic, mildly compressible byte slice so blocks
// exercise real deflate rather than trivial runs.
func variedPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/7)
	}

	return b
}

// extractCAB is a minimal CAB/MSZIP reader for tests: Go has no standard CAB
// reader, so we parse the structures we emit and inflate the CK blocks.
func extractCAB(t *testing.T, data []byte) (name string, payload []byte) {
	t.Helper()

	le := binary.LittleEndian

	coffFiles := le.Uint32(data[16:])
	coffCabStart := le.Uint32(data[36:])

	cCFData := le.Uint16(data[40:])
	if tc := le.Uint16(data[42:]); tc != compressionMSZIP {
		t.Fatalf("typeCompress = %d, want MSZIP", tc)
	}

	cbFile := le.Uint32(data[coffFiles:])
	nameStart := coffFiles + cfFileFixedSize
	// #nosec G115 -- test-only reader; the NUL index is a small offset within data.
	nameEnd := nameStart + uint32(bytes.IndexByte(data[nameStart:], 0))
	name = string(data[nameStart:nameEnd])

	off := coffCabStart
	for i := 0; i < int(cCFData); i++ {
		cbData := le.Uint16(data[off+4:])
		cbUncomp := le.Uint16(data[off+6:])

		block := data[off+cfDataHeaderLen : off+cfDataHeaderLen+uint32(cbData)]
		if !bytes.HasPrefix(block, mszipSignature[:]) {
			t.Fatalf("block %d missing CK signature", i)
		}
		// Each block is an independent deflate stream.
		fr := flate.NewReader(bytes.NewReader(block[len(mszipSignature):]))

		chunk := make([]byte, cbUncomp)
		if _, err := io.ReadFull(fr, chunk); err != nil {
			t.Fatalf("inflate block %d: %v", i, err)
		}

		_ = fr.Close()

		payload = append(payload, chunk...)
		off += cfDataHeaderLen + uint32(cbData)
	}

	if int(cbFile) != len(payload) {
		t.Fatalf("cbFile = %d, but inflated %d bytes", cbFile, len(payload))
	}

	return name, payload
}

// memFile is an in-memory io.WriteSeeker for exercising the back-patching path
// without touching disk.
type memFile struct {
	buf []byte
	pos int64
}

func (m *memFile) Write(p []byte) (int, error) {
	end := m.pos + int64(len(p))
	if end > int64(len(m.buf)) {
		m.buf = append(m.buf, make([]byte, end-int64(len(m.buf)))...)
	}

	copy(m.buf[m.pos:], p)
	m.pos = end

	return len(p), nil
}

func (m *memFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		m.pos = offset
	case io.SeekCurrent:
		m.pos += offset
	case io.SeekEnd:
		m.pos = int64(len(m.buf)) + offset
	}

	return m.pos, nil
}
