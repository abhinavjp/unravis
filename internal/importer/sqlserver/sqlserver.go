// Package sqlserver holds the offline SQL Server importers of card P3
// (importer wave plan §2): the system_health Extended Events CSV (xe.go) and
// the ERRORLOG (errorlog.go). PDH counters reuse the P6 wincounters importer
// (pdh.go, after P6 merges).
//
// Privacy (§4): only allowlisted fields leave the parser. SQL text, input
// buffers, XML reports, host, user and database names never do: a CSV column
// that could carry them quarantines the whole file, and ERRORLOG message text
// is classified by allowlist and never stored. Errors and reports carry
// reasons, counts and header names only, never a cell or a line.
//
// These parsers are built and tested on synthetic files only. Running them on
// a real export stays gated by R12 (the DBA dry run confirms the scrub list).
package sqlserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// effectiveLimits fills zero fields of the caller's limits from the importer
// defaults, and never lets a caller raise a cap above the default.
func effectiveLimits(given, def importer.Limits) importer.Limits {
	l := given
	if l.MaxBytes <= 0 || l.MaxBytes > def.MaxBytes {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxRows <= 0 || l.MaxRows > def.MaxRows {
		l.MaxRows = def.MaxRows
	}
	if l.MaxLineBytes <= 0 || l.MaxLineBytes > def.MaxLineBytes {
		l.MaxLineBytes = def.MaxLineBytes
	}
	if l.MaxFields <= 0 || l.MaxFields > def.MaxFields {
		l.MaxFields = def.MaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = def.MaxRejectFraction
	}
	return l
}

// sink counts rows and rejects and stamps provenance on every emitted event.
type sink struct {
	ctx      context.Context
	opts     importer.Options
	name     string
	limits   importer.Limits
	emit     func(evidence.Event) error
	rep      importer.Report
	first    time.Time
	last     time.Time
	haveSpan bool
}

func newSink(ctx context.Context, name string, opts importer.Options, limits importer.Limits, emit func(evidence.Event) error) *sink {
	return &sink{ctx: ctx, opts: opts, name: name, limits: limits, emit: emit, rep: importer.Report{Importer: name}}
}

// row counts one source row, enforcing the row cap and ctx cancellation.
func (s *sink) row() error {
	s.rep.Rows++
	if s.rep.Rows > s.limits.MaxRows {
		return &importer.LimitError{Limit: "rows", Max: s.limits.MaxRows, At: s.rep.Rows}
	}
	if s.rep.Rows&127 == 0 {
		return s.ctx.Err()
	}
	return nil
}

func (s *sink) reject(line int64, r importer.Reason) { s.rep.Reject(line, r) }

// seen widens source coverage with a valid row time (classified or not).
func (s *sink) seen(t time.Time) {
	if !s.haveSpan || t.Before(s.first) {
		s.first = t
	}
	if !s.haveSpan || t.After(s.last) {
		s.last = t
	}
	s.haveSpan = true
}

func (s *sink) put(e evidence.Event, line int64, ordinal int) error {
	e.Source = s.name
	e.Line = line
	e.EvidenceID = evidence.EventID(s.opts.CaseID, s.name, s.opts.ImportNonce, line, ordinal)
	s.rep.Emitted++
	return s.emit(e)
}

// finish applies the reject-fraction rule and fills coverage.
func (s *sink) finish() (importer.Report, error) {
	if s.haveSpan {
		s.rep.First, s.rep.Last = s.first, s.last
		f, l := s.first, s.last
		s.rep.Quality.CoverageFirst, s.rep.Quality.CoverageLast = &f, &l
	}
	if s.rep.Rows > 0 && float64(s.rep.Rejected)/float64(s.rep.Rows) > s.limits.MaxRejectFraction {
		return s.rep, &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
	}
	return s.rep, nil
}

// sniffed peeks the head, refuses archives, and returns a reader that still
// starts at byte 0.
func sniffed(r io.Reader) (*bufio.Reader, []byte, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, nil, err
	}
	if err := importer.Sniff(head); err != nil {
		return nil, nil, err
	}
	return br, head, nil
}

// looksUTF16LE reports a BOM-less UTF-16LE head: ASCII text with a zero in
// every odd byte. ERRORLOG copies sometimes lose the BOM.
func looksUTF16LE(head []byte) bool {
	if len(head) < 8 || bytes.HasPrefix(head, []byte{0xff, 0xfe}) || bytes.HasPrefix(head, []byte{0xfe, 0xff}) {
		return false
	}
	n := len(head) &^ 1
	if n > 256 {
		n = 256
	}
	zeros := 0
	for i := 1; i < n; i += 2 {
		if head[i] == 0 && head[i-1] != 0 {
			zeros++
		}
	}
	return zeros*10 >= (n/2)*9
}

// headText decodes up to 4 KiB of a file head for Detect: UTF-8, UTF-8 BOM,
// UTF-16LE/BE BOM and BOM-less UTF-16LE.
func headText(head []byte) string {
	if len(head) > 4096 {
		head = head[:4096]
	}
	var dec transform.Transformer
	switch {
	case bytes.HasPrefix(head, []byte{0xef, 0xbb, 0xbf}):
		return string(head[3:])
	case bytes.HasPrefix(head, []byte{0xff, 0xfe}):
		dec = unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()
	case bytes.HasPrefix(head, []byte{0xfe, 0xff}):
		dec = unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder()
	case looksUTF16LE(head):
		dec = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	default:
		return string(head)
	}
	out, _, _ := transform.Bytes(dec, head[:len(head)&^1])
	return string(out)
}

// firstLines returns up to n non-empty lines of s, without line endings.
func firstLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return out
}

func ptr(f float64) *float64 { return &f }

// isToken reports a short vocabulary value (wait type, component, state):
// letters, digits and underscores only. Anything else is never stored.
func isToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// sqlSeverity maps a SQL Server error severity (0..25) to the event severity.
func sqlSeverity(sev int) string {
	switch {
	case sev >= 20:
		return "critical"
	case sev >= 17:
		return "error"
	case sev >= 11:
		return "warning"
	}
	return "info"
}
