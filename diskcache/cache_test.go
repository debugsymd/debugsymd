package diskcache

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitThenLookup(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Miss before write: nil file, nil info, nil error — never an error.
	f, fi, err := c.Lookup("cab_synth", "ABC123", "foo.pdb")
	if f != nil || fi != nil || err != nil {
		t.Fatalf("miss = (%v,%v,%v), want all nil", f, fi, err)
	}

	tmp, err := c.NewTemp()
	if err != nil {
		t.Fatal(err)
	}

	want := []byte("cabinet bytes")
	if _, err := tmp.Write(want); err != nil {
		t.Fatal(err)
	}

	if err := c.Commit(tmp, "cab_synth", "ABC123", "foo.pdb"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(tmp.Name()); !os.IsNotExist(err) {
		t.Fatalf("staging file should be gone after commit, stat err = %v", err)
	}

	f, fi, err = c.Lookup("cab_synth", "ABC123", "foo.pdb")
	if err != nil {
		t.Fatal(err)
	}

	if f == nil || fi == nil {
		t.Fatal("expected a hit after commit")
	}

	t.Cleanup(func() { _ = f.Close() })

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Fatalf("content = %q, want %q", got, want)
	}

	// A different role/inner must not collide.
	if f2, _, _ := c.Lookup("objects", "ABC123", "foo.pdb"); f2 != nil {
		t.Fatal("different role should miss")
	}
}

func TestEviction(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	tmp, err := c.NewTemp()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tmp.WriteString("stale"); err != nil {
		t.Fatal(err)
	}

	if err := c.Commit(tmp, "objects", "OLD", "x.pdb"); err != nil {
		t.Fatal(err)
	}

	// Nothing is older than a day yet.
	if n, err := c.Evict(24 * time.Hour); err != nil || n != 0 {
		t.Fatalf("Evict early = (%d,%v), want (0,nil)", n, err)
	}
	// Everything is older than zero.
	if n, err := c.Evict(0); err != nil || n != 1 {
		t.Fatalf("Evict all = (%d,%v), want (1,nil)", n, err)
	}

	if f, _, _ := c.Lookup("objects", "OLD", "x.pdb"); f != nil {
		t.Fatal("entry should be gone after eviction")
	}
}

func TestEvictionPrunesEmptyDirs(t *testing.T) {
	root := t.TempDir()

	c, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	tmp, err := c.NewTemp()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tmp.WriteString("stale"); err != nil {
		t.Fatal(err)
	}

	if err := c.Commit(tmp, "objects", "OLD", "x.pdb"); err != nil {
		t.Fatal(err)
	}

	// Evicting the only file should also remove the empty objects/<hh>/<hh> shard
	// tree, leaving just the structural tmp staging dir behind.
	if n, err := c.Evict(0); err != nil || n != 1 {
		t.Fatalf("Evict = (%d,%v), want (1,nil)", n, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.Name() != tmpSubdir {
			t.Fatalf("unexpected leftover under cache root: %q", e.Name())
		}
	}

	// The tmp staging dir must survive so future writes can still stage.
	if _, err := os.Stat(filepath.Join(root, tmpSubdir)); err != nil {
		t.Fatalf("tmp staging dir was pruned: %v", err)
	}
}

func TestProbe(t *testing.T) {
	root := t.TempDir()

	c, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Probe(); err != nil {
		t.Fatalf("Probe on a healthy cache = %v, want nil", err)
	}

	// A vanished cache directory (unmounted volume, wiped path) must fail the probe.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	if err := c.Probe(); err == nil {
		t.Fatal("Probe on an unwritable cache = nil, want error")
	}
}
