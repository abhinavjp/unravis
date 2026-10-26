package casestore

import "context"

// MaxLedgerBlob caps the theories ledger a case keeps: ledger.MaxTotalBytes
// (8 MiB, the size of all entries as the API lists them) plus 1 MiB for the JSON
// structure around them. casestore does not import the ledger package; a test in
// the server package checks that this stays at least ledger.MaxTotalBytes.
const MaxLedgerBlob = 9 << 20

// LedgerBlobStore is implemented by a Case that keeps its own theories ledger.
// The store holds the ledger's JSON as opaque bytes (it neither parses nor
// validates them: internal/ledger does), so a case holds everything and the
// theories travel with its case file. It is a separate interface, like
// HealthStore, so it breaks no Case.
type LedgerBlobStore interface {
	// LedgerBlob returns the stored bytes. A case that has none (or an older case
	// without the table) returns nil, not an error. The slice is the caller's.
	LedgerBlob(ctx context.Context) ([]byte, error)
	// SetLedgerBlob replaces the stored bytes. ErrCapExceeded for more than
	// MaxLedgerBlob bytes. Refused with ErrCaseClosed once the case is closed.
	SetLedgerBlob(ctx context.Context, b []byte) error
}
