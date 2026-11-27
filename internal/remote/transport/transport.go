// Package transport is the one interface the remote layer reads a server through. A transport never writes to the server: it lists, reads and hashes
// files that sit inside the folders the person allowed, and nothing else.
package transport

import (
	"context"
	"errors"
	"io"
	"time"
)

// HostInfo is what a connection test learns about a server.
type HostInfo struct {
	OS          string        `json:"os"`          // windows or linux
	Description string        `json:"description"` // e.g. "Linux 5.15.0" or the Windows caption
	ClockOffset time.Duration `json:"clockOffset"` // server clock minus this PC's clock, ignoring transit time
}

// FileInfo is one file in a folder listing.
type FileInfo struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// Transport reads a server. Every path must lie inside the allowed folders the transport was given; anything else is ErrNotAllowed.
type Transport interface {
	Probe(ctx context.Context) (HostInfo, error)
	List(ctx context.Context, dir string) ([]FileInfo, error)
	// Open streams the file from byte offset off. A stream that ends early or fails reports an error at the end; it never looks complete.
	Open(ctx context.Context, path string, off int64) (io.ReadCloser, error)
	// Digest is the lowercase hex SHA-256 of the whole file, computed on the server.
	Digest(ctx context.Context, path string) (string, error)
	Close() error
}

// ErrNotAllowed means a path is outside the folders the person allowed (or is not a plain path).
var ErrNotAllowed = errors.New("That path is outside the folders you allowed.") //nolint:staticcheck // shown to the person as a sentence
