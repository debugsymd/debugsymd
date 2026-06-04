package objects

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/debugsymd/debugsymd/cab"
	"github.com/debugsymd/debugsymd/resolver"
)

// FetchCompressed returns a CAB-wrapped form of the object for serving the
// Microsoft compressed symbol forms (.pd_/.dl_/.ex_). req.Filename is the
// uncompressed name embedded in the cabinet (e.g. "foo.pdb"). Resolution order:
//
//  1. raw_compressed cache (a byte-identical upstream CAB), then
//  2. cab_synth cache (a previously synthesized envelope), then
//  3. produce one — under singleflight — by ensuring the object is downloaded,
//     promoting it to raw_compressed if it is already a CAB, otherwise
//     synthesizing an MSZIP cabinet into cab_synth.
//
// The caller owns the returned file.
func (s *Service) FetchCompressed(ctx context.Context, req resolver.Request) (*os.File, fs.FileInfo, error) {
	id, inner := cacheKey(req)

	// Fast path: already mirrored or synthesized.
	if file, info, err := s.lookupCompressed(id, inner); err != nil || file != nil {
		return file, info, err
	}

	// Produce it once across concurrent callers, then re-open whichever role the
	// producer populated (raw_compressed if the upstream was a CAB, else cab_synth).
	// Decouple the shared synthesis from any single caller's context (see object):
	// a client disconnect must not fail the other coalesced callers.
	work := context.WithoutCancel(ctx)

	if _, err, _ := s.group.Do(flightKey(roleCabSynth, id, inner), func() (any, error) {
		return nil, s.synthesize(work, req)
	}); err != nil {
		return nil, nil, fmt.Errorf("synthesizing cabinet: %w", err)
	}

	return s.lookupCompressed(id, inner)
}

// lookupCompressed returns the cached compressed form for the (id, inner) cache
// key from the raw_compressed mirror or, failing that, the cab_synth cache. A
// miss in both returns (nil, nil, nil).
func (s *Service) lookupCompressed(id, inner string) (*os.File, fs.FileInfo, error) {
	for _, role := range []string{roleRawCompressed, roleCabSynth} {
		file, info, err := s.cache.Lookup(role, id, inner)
		if err != nil {
			return nil, nil, fmt.Errorf("looking up %s cache: %w", role, err)
		}

		if file != nil {
			return file, info, nil
		}
	}

	return nil, nil, nil
}

// synthesize ensures the raw object is cached, then either promotes it to the
// raw_compressed mirror (if the upstream payload is itself a CAB) or wraps it in
// a freshly synthesized MSZIP cabinet under cab_synth. Streamed end to end.
func (s *Service) synthesize(ctx context.Context, req resolver.Request) error {
	obj, info, err := s.object(ctx, req)
	if err != nil {
		return err
	}

	defer func() { _ = obj.Close() }()

	isCab, magicErr := startsWithCabMagic(obj)
	if magicErr != nil {
		return magicErr
	}

	if _, err := obj.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewinding object: %w", err)
	}

	tmp, tmpErr := s.cache.NewTemp()
	if tmpErr != nil {
		return fmt.Errorf("creating staging file: %w", tmpErr)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	role := roleCabSynth

	if isCab {
		// Already a cabinet: mirror the bytes verbatim.
		if _, err := io.Copy(tmp, obj); err != nil {
			return fmt.Errorf("mirroring upstream cabinet: %w", err)
		}

		role = roleRawCompressed
	} else if err := cab.Write(tmp, req.Filename, info.Size(), obj); err != nil {
		return fmt.Errorf("synthesizing cabinet: %w", err)
	}

	id, inner := cacheKey(req)
	if err := s.cache.Commit(tmp, role, id, inner); err != nil {
		return fmt.Errorf("committing cabinet to cache: %w", err)
	}

	committed = true

	return nil
}

// startsWithCabMagic reports whether r begins with the CAB signature. It reads
// from the current position; callers seek back afterwards.
func startsWithCabMagic(r io.Reader) (bool, error) {
	var magic [len(cabMagic)]byte

	n, err := io.ReadFull(r, magic[:])
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return false, nil // too short to be a cabinet
	}

	if err != nil {
		return false, fmt.Errorf("reading object magic: %w", err)
	}

	return string(magic[:n]) == cabMagic, nil
}
