package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the fully-resolved daemon configuration.
type Config struct {
	// Bind is the address the public symstore listener binds to.
	Bind string
	// Admin is the metrics/health listener (expvar, /healthz, /readyz), kept off
	// the public surface on purpose.
	Admin string

	// CacheDir is the root of the on-disk object/raw_compressed/cab_synth caches.
	CacheDir string
	// CacheMaxUnusedFor is how long an entry may sit untouched before the sweeper removes it.
	CacheMaxUnusedFor time.Duration

	Sentry SentryConfig
	// Storage is the S3 (or S3-compatible) backend configuration.
	Storage StorageConfig

	// LogLevel is one of debug, info, warn, error.
	LogLevel string

	// Debuginfod enables the debuginfod-compatible build-id routes alongside symstore.
	Debuginfod bool
}

// SentryConfig configures the Sentry REST resolver.
type SentryConfig struct {
	APIURL  string
	Org     string
	Project string
	// Token comes from SENTRY_AUTH_TOKEN, never a flag, so it stays out of
	// process listings and shell history.
	Token string
}

// StorageConfig configures the S3 backend. EndpointURL is set only for
// S3-compatible backends (MinIO, Ceph, etc.). KeyPrefix is prepended to the
// blob key when the symbol store does not sit at the bucket root.
type StorageConfig struct {
	Bucket      string
	Region      string
	EndpointURL string
	KeyPrefix   string
}

const (
	defaultBind      = ":8080"
	defaultAdmin     = ":9090"
	defaultCacheDir  = "/var/cache/debugsymd"
	defaultMaxUnused = 14 * 24 * time.Hour
	defaultSentryAPI = ""
	defaultLogLevel  = "info"
	// These are env var NAMES, not credentials (the _FILE form points at a
	// Docker/Kubernetes secret mount).
	sentryTokenEnvName     = "SENTRY_AUTH_TOKEN"      // #nosec G101 -- env var name, not a credential.
	sentryTokenFileEnvName = "SENTRY_AUTH_TOKEN_FILE" // #nosec G101 -- env var name, not a credential.
)

// Load parses the given arguments (typically os.Args[1:]) into a Config. The
// returned FlagSet is exposed so callers can render usage on error.
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("debugsymd", flag.ContinueOnError)

	c := &Config{}
	fs.StringVar(&c.Bind, "bind", defaultBind, "address for the public symstore listener")
	fs.StringVar(&c.Admin, "admin", defaultAdmin, "address for the metrics/health listener")
	fs.StringVar(&c.CacheDir, "cache-dir", defaultCacheDir, "root directory for the on-disk caches")
	fs.DurationVar(&c.CacheMaxUnusedFor, "cache-max-unused-for", defaultMaxUnused, "evict cache entries untouched for longer than this")

	fs.StringVar(&c.Sentry.APIURL, "sentry-api-url", defaultSentryAPI, "Sentry API base URL, e.g. https://sentry.internal/api/0")
	fs.StringVar(&c.Sentry.Org, "sentry-org", "", "Sentry organization slug")
	fs.StringVar(&c.Sentry.Project, "sentry-project", "", "Sentry project slug")

	fs.StringVar(&c.Storage.Bucket, "s3-bucket", "", "S3 bucket holding the symbol store")
	fs.StringVar(&c.Storage.Region, "s3-region", "", "S3 region")
	fs.StringVar(&c.Storage.EndpointURL, "s3-endpoint-url", "", "optional S3-compatible endpoint URL")
	fs.StringVar(&c.Storage.KeyPrefix, "s3-key-prefix", "", "optional prefix prepended to derived blob keys")

	fs.StringVar(&c.LogLevel, "log-level", defaultLogLevel, "log level: debug, info, warn, error")
	fs.BoolVar(&c.Debuginfod, "debuginfod", true, "serve the debuginfod build-id routes (/buildid/<id>/...)")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("parsing flags: %w", err)
	}

	token, tokenErr := loadSecret(sentryTokenFileEnvName, sentryTokenEnvName)
	if tokenErr != nil {
		return nil, tokenErr
	}

	c.Sentry.Token = token

	if err := c.validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// loadSecret reads a secret from the file named by fileEnv (the *_FILE convention
// used by Docker/Kubernetes secret mounts) when that env var is set, otherwise
// from the plain valueEnv. A file's trailing whitespace/newline is trimmed. A
// set-but-unreadable file is a hard error rather than a silent fall back to env,
// so a misconfigured secret mount fails loudly instead of running unauthenticated.
func loadSecret(fileEnv, valueEnv string) (string, error) {
	path := os.Getenv(fileEnv)
	if path == "" {
		return os.Getenv(valueEnv), nil
	}

	b, err := os.ReadFile(path) // #nosec G304 G703 -- operator-provided secret-mount path, not request input.
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", fileEnv, err)
	}

	return strings.TrimSpace(string(b)), nil
}

func (c *Config) validate() error {
	if c.CacheDir == "" {
		return errors.New("cache-dir must not be empty")
	}

	if c.Sentry.APIURL != "" {
		if c.Sentry.Org == "" || c.Sentry.Project == "" {
			return errors.New("sentry-org and sentry-project are required when sentry-api-url is set")
		}

		if c.Sentry.Token == "" {
			return fmt.Errorf("%s must be set when sentry-api-url is set", sentryTokenEnvName)
		}
	}

	if c.Storage.Bucket == "" {
		return errors.New("s3-bucket is required")
	}

	return nil
}
