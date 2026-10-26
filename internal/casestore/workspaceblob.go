package casestore

import "context"

// MaxWorkspaceBlob caps the workspace note a case keeps: a small JSON object the
// web UI uses to remember a non-secret reference to the case's Mongo connection
// and databases, and the last investigation filters.
const MaxWorkspaceBlob = 64 << 10

// WorkspaceBlobStore is implemented by a Case that keeps its own workspace note.
// The store holds the JSON as opaque bytes (it neither parses nor validates
// them: the API layer does), so the note travels with the case file. It is a
// separate interface, like LedgerBlobStore, so it breaks no Case.
type WorkspaceBlobStore interface {
	// WorkspaceBlob returns the stored bytes. A case that has none (or an older
	// case without the table) returns nil, not an error. The slice is the caller's.
	WorkspaceBlob(ctx context.Context) ([]byte, error)
	// SetWorkspaceBlob replaces the stored bytes; nil or empty clears them.
	// ErrCapExceeded for more than MaxWorkspaceBlob bytes. Refused with
	// ErrCaseClosed once the case is closed.
	SetWorkspaceBlob(ctx context.Context, b []byte) error
}
