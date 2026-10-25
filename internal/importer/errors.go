package importer

import (
	"errors"
	"regexp"
	"strconv"
)

// Sentinel errors. Test with errors.Is.
var (
	ErrLimit              = errors.New("limit exceeded")
	ErrQuarantine         = errors.New("file quarantined")
	ErrTimezoneUnlabelled = errors.New("time zone not stated")
	ErrUnknownHost        = errors.New("unknown host")
	// ErrOutOfWindow ends an import whose events all lie outside the requested
	// time window. Nothing is stored, so a later pull with a wider window can
	// still read the same bytes.
	ErrOutOfWindow = errors.New("no events inside the time window")
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

// columnPattern is the shape of a canonical column name: letters, digits and
// "_", "." or "-", starting with a letter, at most 64 bytes. Spaces, brackets and
// anything else mean the text did not come from an importer's fixed list.
var columnPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// SafeColumn returns column when it has the shape of a canonical column name,
// else "". It is the backstop between an importer and every place a quarantine
// Column is stored, logged or sent: raw file header text must never get through
// (section 4 rule 9), whatever an importer put in the field.
func SafeColumn(column string) string {
	if columnPattern.MatchString(column) {
		return column
	}
	return ""
}

// QuarantineError refuses the whole file. Column is a header name only, and
// only a canonical name from the importer's own fixed lists, never the header
// text of the file. Consumers pass it through SafeColumn.
type QuarantineError struct {
	Reason Reason
	Column string
}

func (e *QuarantineError) Error() string {
	s := "file quarantined: " + e.Reason.Text()
	if col := SafeColumn(e.Column); col != "" {
		s += " (column " + strconv.Quote(col) + ")"
	}
	return s
}

// Unwrap makes errors.Is(err, ErrQuarantine) true.
func (e *QuarantineError) Unwrap() error { return ErrQuarantine }
