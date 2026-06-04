// Package resolver maps a debug identifier to the location of its object bytes
// in the symbol store. The default implementation talks to the Sentry REST API;
// a direct-Postgres implementation can be added behind the same interface.
package resolver

import (
	"context"
	"errors"
)

// ErrNotFound reports that no object matches the request. Callers translate it
// to an HTTP 404.
var ErrNotFound = errors.New("resolver: object not found")

// FileType is the kind of debug-information file being requested. It mirrors the
// object types a symbol store distinguishes (Windows PE/PDB, native ELF/Mach-O
// code vs debug, and Sentry source bundles).
type FileType string

const (
	FilePDB          FileType = "pdb"
	FilePE           FileType = "pe"
	FileELFCode      FileType = "elf_code"
	FileELFDebug     FileType = "elf_debug"
	FileMachCode     FileType = "mach_code"
	FileMachDebug    FileType = "mach_debug"
	FileSourceBundle FileType = "sourcebundle"
)

// Request identifies the object to resolve. Windows objects are keyed by
// DebugID; native (ELF/Mach-O) objects are keyed by CodeID (the GNU build-id or
// Mach-O UUID). At least one of DebugID/CodeID must be set. Filename is the
// module basename when known (it may be a placeholder such as "_.debug" for the
// native symstore forms, in which case selection relies on the ID and FileType).
type Request struct {
	DebugID  string
	CodeID   string
	Filename string
	FileType FileType
}

// Location is where an object's bytes live in the backing store.
type Location struct {
	Bucket      string
	Key         string
	Size        int64
	ContentType string
}

// Resolver resolves a Request to a storage Location.
type Resolver interface {
	Lookup(ctx context.Context, req Request) (*Location, error)
}
