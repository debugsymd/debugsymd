package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSecretFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The file form wins over the plain env var, and trailing newline is trimmed.
	t.Setenv("X_FILE", path)
	t.Setenv("X", "from-env")

	got, err := loadSecret("X_FILE", "X")
	if err != nil {
		t.Fatal(err)
	}

	if got != "s3cr3t" {
		t.Fatalf("got %q, want trimmed file contents", got)
	}
}

func TestLoadSecretFromEnv(t *testing.T) {
	t.Setenv("X_FILE", "")
	t.Setenv("X", "from-env")

	got, err := loadSecret("X_FILE", "X")
	if err != nil || got != "from-env" {
		t.Fatalf("got (%q, %v), want (from-env, nil)", got, err)
	}
}

func TestLoadSecretMissingFile(t *testing.T) {
	// A set-but-unreadable secret mount must fail loudly, not fall back to env.
	t.Setenv("X_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("X", "from-env")

	if _, err := loadSecret("X_FILE", "X"); err == nil {
		t.Fatal("unreadable secret file: want error, got nil")
	}
}
