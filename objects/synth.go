package objects

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/debugsymd/debugsymd/cab"
	"github.com/debugsymd/debugsymd/metrics"
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
	file, info, role, err := s.lookupCompressed(id, inner)
	if err != nil {
		return nil, nil, err
	}

	if file != nil {
		metrics.CacheLookups.WithLabelValues(role, metrics.ResultHit).Inc()
		slog.InfoContext(ctx, "compressed cache hit", "role", role, "id", id, "inner", inner)

		return file, info, nil
	}

	metrics.CacheLookups.WithLabelValues(labelCompressed, metrics.ResultMiss).Inc()
	slog.InfoContext(ctx, "compressed cache miss, producing", "id", id, "inner", inner)

	// Produce it once across concurrent callers, then re-open whichever role the
	// producer populated (raw_compressed if the upstream was a CAB, else cab_synth).
	if err := s.produceOnce(ctx, flightKey(roleCabSynth, id, inner), func(ctx context.Context) error {
		return s.synthesize(ctx, req)
	}); err != nil {
		return nil, nil, err
	}

	// Re-open is always a hit and is not counted (see object).
	file, info, _, err = s.lookupCompressed(id, inner)

	return file, info, err
}

// lookupCompressed returns the cached compressed form for the (id, inner) cache
// key from the raw_compressed mirror or, failing that, the cab_synth cache,
// along with the role that served it. A miss in both returns ("", nil, nil, nil).
func (s *Service) lookupCompressed(id, inner string) (*os.File, fs.FileInfo, string, error) {
	for _, role := range []string{roleRawCompressed, roleCabSynth} {
		file, info, err := s.cache.Lookup(role, id, inner)
		if err != nil {
			return nil, nil, "", fmt.Errorf("looking up %s cache: %w", role, err)
		}

		if file != nil {
			return file, info, role, nil
		}
	}

	return nil, nil, "", nil
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

		slog.InfoContext(ctx, "mirrored upstream cabinet", "filename", req.Filename, "size", info.Size())
	} else {
		synthStart := time.Now()

		if err := cab.Write(tmp, req.Filename, info.Size(), obj); err != nil {
			return fmt.Errorf("synthesizing cabinet: %w", err)
		}

		synthSeconds := time.Since(synthStart).Seconds()
		metrics.CABSynthDuration.Observe(synthSeconds)
		metrics.CABSynthBytesTotal.Add(float64(info.Size()))

		slog.InfoContext(ctx, "synthesized cabinet",
			"filename", req.Filename,
			"size", info.Size(),
			"synth_seconds", synthSeconds,
		)
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
