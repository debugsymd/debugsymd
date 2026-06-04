// Package storage streams object bytes out of the backing blob store. The S3
// implementation is the default; the Fetcher interface keeps the proxy
// decoupled from it so a local or S3-compatible backend can be swapped in.
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound reports that the requested key does not exist in the store. This
// normally signals an inconsistency (the resolver named a key the store lacks)
// and maps to an HTTP 404.
var ErrNotFound = errors.New("storage: key not found")

// Fetcher streams the object at (bucket, key) into w and returns the number of
// bytes copied. Implementations must stream rather than buffer: objects can be
// many gigabytes.
type Fetcher interface {
	Fetch(ctx context.Context, bucket, key string, w io.Writer) (int64, error)
}
