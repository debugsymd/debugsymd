package diskcache

import "io/fs"

const (
	dirPerm fs.FileMode = 0o755
	// tmpSubdir holds in-progress writes before they are atomically renamed into
	// their final sharded location.
	tmpSubdir = "tmp"
	// tmpPattern is the os.CreateTemp prefix for staged writes.
	tmpPattern = "staging-"
)
