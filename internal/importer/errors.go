package importer

import (
	"errors"
	"strconv"
)

// Sentinel errors. Test with errors.Is.
var (
	ErrLimit              = errors.New("limit exceeded")
	ErrQuarantine         = errors.New("file quarantined")
	ErrTimezoneUnlabelled = errors.New("time zone not stated")
	ErrUnknownHost        = errors.New("unknown host")
)

// LimitError says which limit was passed. Limit is a fixed word ("bytes",
// "rows", "events", "line-bytes", "fields", "hosts"); At is the first value over Max
// (a byte count, row count, line number or field count).
type LimitError struct {
	Limit   string
	Max, At int64
}

func (e *LimitError) Error() string {
	return "limit exceeded: " + e.Limit + " (max " + strconv.FormatInt(e.Max, 10) + ", reached " + strconv.FormatInt(e.At, 10) + ")"
}

// Unwrap makes errors.Is(err, ErrLimit) true.
func (e *LimitError) Unwrap() error { return ErrLimit }

// QuarantineError refuses the whole file. Column is a header name only, and
// only a name from the importer's own fixed lists.
type QuarantineError struct {
	Reason Reason
	Column string
}

func (e *QuarantineError) Error() string {
	s := "file quarantined: " + e.Reason.Text()
	if e.Column != "" {
		s += " (column " + strconv.Quote(e.Column) + ")"
	}
	return s
}

// Unwrap makes errors.Is(err, ErrQuarantine) true.
func (e *QuarantineError) Unwrap() error { return ErrQuarantine }
