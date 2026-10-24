package evidence

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
	"errors"
	"time"
)

// ErrInvalid is returned for an Input that cannot be exported.
var ErrInvalid = errors.New("invalid export input")

// Input is everything Build needs. Key is the per-case pseudonym key; it is used
// to derive pseudonyms and is never written to any output.
type Input struct {
	Report       audit.PatternReport
	SourceLabels map[string]string // source id -> human label; ids never appear in an export
	Ledger       []ledger.Hypothesis
	Options      Options
	ToolVersion  string
	Now          time.Time
	Key          []byte
}

// NewKey returns a fresh random 32-byte per-case key.
func NewKey() ([]byte, error) { return nil, errors.New("not implemented") }

// Build assembles the preview/export. Implemented by the P13 engine task.
func Build(in Input) (Export, error) { return Export{}, errors.New("not implemented") }

// Canonical returns the exact bytes of the export file (indented JSON, stable key order).
func Canonical(e Export) ([]byte, error) { return nil, errors.New("not implemented") }

// Verify re-parses a file's bytes and recomputes the body hash against the manifest.
func Verify(raw []byte) error { return errors.New("not implemented") }
