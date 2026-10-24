// Package wincsv holds what the two Windows CSV importers (wincounters and
// winevents) share: input preparation (byte cap, archive and binary refusal,
// BOM decoding, line and record guards), limit defaults, and report
// accumulation. Nothing here reads a cell into an error or a log.
package wincsv

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"sort"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// Section 4 defaults applied when a Limits field is zero.
const (
	DefaultMaxLineBytes = 64 << 10
	DefaultMaxFields    = 4096
	DefaultRejectFrac   = 0.05
	// RecordFactor bounds a quoted multi-line record to this many line caps, so an
	// unterminated quote cannot pull a whole file into memory.
	RecordFactor = 16
	// MaxObservedHosts bounds Report.ObservedHosts.
	MaxObservedHosts = 1000
	maxRowErrors     = 100
	maxAttrBytes     = 256
)

// evtxMagic is the Windows event log (.evtx) file signature. A .blg performance
// log has no text header; binary input of any kind is caught by the NUL check.
var evtxMagic = []byte("ElfFile\x00")

// MergeLimits fills zero fields of l from def, then from the section 4 defaults.
func MergeLimits(l, def importer.Limits) importer.Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxRows <= 0 {
		l.MaxRows = def.MaxRows
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = def.MaxLineBytes
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = DefaultMaxLineBytes
	}
	if l.MaxFields <= 0 {
		l.MaxFields = def.MaxFields
	}
	if l.MaxFields <= 0 {
		l.MaxFields = DefaultMaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = def.MaxRejectFraction
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = DefaultRejectFrac
	}
	return l
}

// Prepare applies the byte cap, refuses archives and binary files by magic, decodes a
// UTF-8 or UTF-16 BOM, and guards line and record length. The result is plain
// UTF-8 text with no BOM. lim must already be merged.
func Prepare(r io.Reader, lim importer.Limits) (io.Reader, error) {
	if lim.MaxBytes > 0 {
		r = importer.LimitReader(r, lim.MaxBytes)
	}
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	if e := importer.Sniff(head); e != nil {
		return nil, e
	}
	utf16BOM := bytes.HasPrefix(head, []byte{0xFF, 0xFE}) || bytes.HasPrefix(head, []byte{0xFE, 0xFF})
	if bytes.HasPrefix(head, evtxMagic) || (!utf16BOM && bytes.IndexByte(head, 0) >= 0) {
		return nil, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	// encoding.Nop keeps invalid UTF-8 intact so textio's strict check can see it.
	dec := transform.NewReader(br, unicode.BOMOverride(encoding.Nop.NewDecoder()))
	return &guard{r: dec, maxLine: lim.MaxLineBytes, maxRec: lim.MaxLineBytes * RecordFactor}, nil
}

// guard enforces the physical line cap and the quoted-record cap on decoded text.
type guard struct {
	r         io.Reader
	maxLine   int
	maxRec    int
	line, rec int
	lines     int64
	inQuote   bool
	err       error
}

func (g *guard) Read(p []byte) (int, error) {
	if g.err != nil { // sticky: a reader above that swallows one error must not see data after it
		return 0, g.err
	}
	n, err := g.r.Read(p)
	if err != nil && err != io.EOF {
		g.err = err
	}
	for _, c := range p[:n] {
		switch c {
		case '"':
			g.inQuote = !g.inQuote
			g.line++
			g.rec++
		case '\n':
			g.lines++
			g.line = 0
			if g.inQuote {
				g.rec++
			} else {
				g.rec = 0
			}
			continue
		default:
			g.line++
			g.rec++
		}
		if g.line > g.maxLine {
			g.err = &importer.LimitError{Limit: "line", Max: int64(g.maxLine), At: g.lines + 1}
			return 0, g.err
		}
		if g.rec > g.maxRec {
			g.err = &importer.LimitError{Limit: "record", Max: int64(g.maxRec), At: g.lines + 1}
			return 0, g.err
		}
	}
	return n, err
}

// OpenCSV is Prepare followed by textio.NewCSV. Empty or header-less input is
// reported as unsupported-format.
func OpenCSV(r io.Reader, lim importer.Limits, comment rune) (*textio.CSVReader, error) {
	pr, err := Prepare(r, lim)
	if err != nil {
		return nil, err
	}
	cr, _, err := textio.NewCSV(pr, textio.CSVOptions{Comma: ',', Comment: comment, MaxFields: lim.MaxFields})
	if err != nil {
		var le *importer.LimitError
		var qe *importer.QuarantineError
		var pe *csv.ParseError
		switch {
		case errors.As(err, &le), errors.As(err, &qe):
			return nil, err
		case errors.Is(err, io.EOF), errors.As(err, &pe), errors.Is(err, textio.ErrEncoding):
			return nil, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		return nil, err
	}
	return cr, nil
}

// RowReason classifies a CSV Next error that rejects one row rather than the file.
func RowReason(err error) (importer.Reason, bool) {
	var pe *csv.ParseError
	switch {
	case errors.As(err, &pe):
		return importer.ReasonFieldCount, true
	case errors.Is(err, textio.ErrEncoding):
		return importer.ReasonEncoding, true
	}
	return "", false
}

// TZUnlabelled is the whole-file refusal for host-local times without a host zone.
// It matches both importer.ErrTimezoneUnlabelled and a *QuarantineError.
func TZUnlabelled() error {
	return errors.Join(&importer.QuarantineError{Reason: importer.ReasonTZUnlabelled}, importer.ErrTimezoneUnlabelled)
}

// MissingColumn refuses a file whose header lacks a required column (header name only).
func MissingColumn(name string) error {
	return &importer.QuarantineError{Reason: importer.ReasonFieldCount, Column: name}
}

// HeadText decodes the first bytes of a file for Detect: BOM sniffed, never an error.
func HeadText(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		head = head[3:]
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		le := head[0] == 0xFF
		head = head[2:]
		u := make([]uint16, 0, len(head)/2)
		for i := 0; i+1 < len(head); i += 2 {
			if le {
				u = append(u, uint16(head[i])|uint16(head[i+1])<<8)
			} else {
				u = append(u, uint16(head[i+1])|uint16(head[i])<<8)
			}
		}
		return string(utf16.Decode(u))
	}
	if !utf8.Valid(head) {
		head = bytes.ToValidUTF8(head, nil)
	}
	return string(head)
}

// ClipAttr bounds an attribute value to 256 bytes on a rune boundary. The value is
// otherwise kept verbatim: escaping happens at export.
func ClipAttr(s string) string {
	if len(s) <= maxAttrBytes {
		return s
	}
	s = s[:maxAttrBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Acc accumulates the Report during a parse.
type Acc struct {
	Rep   importer.Report
	Lim   importer.Limits
	hosts map[string]bool
	gaps  map[string]bool
}

// NewAcc starts a report for importer name.
func NewAcc(name string, lim importer.Limits) *Acc {
	return &Acc{
		Rep:   importer.Report{Importer: name, FieldRules: map[string]string{}},
		Lim:   lim,
		hosts: map[string]bool{},
		gaps:  map[string]bool{},
	}
}

// Row counts one data row and enforces the row cap.
func (a *Acc) Row(line int64) error {
	a.Rep.Rows++
	if a.Lim.MaxRows > 0 && a.Rep.Rows > a.Lim.MaxRows {
		return &importer.LimitError{Limit: "rows", Max: a.Lim.MaxRows, At: line}
	}
	return nil
}

// Reject counts a rejected row with its reason.
func (a *Acc) Reject(line int64, reason importer.Reason) {
	a.Rep.Rejected++
	if len(a.Rep.Errors) < maxRowErrors {
		a.Rep.Errors = append(a.Rep.Errors, importer.RowError{Line: line, Reason: reason})
	}
}

// Observe records a raw host name seen in the file.
func (a *Acc) Observe(host string) {
	if host != "" && len(a.hosts) < MaxObservedHosts {
		a.hosts[host] = true
	}
}

// Gap adds a fixed known-gap sentence once.
func (a *Acc) Gap(s string) {
	if !a.gaps[s] {
		a.gaps[s] = true
		a.Rep.KnownGaps = append(a.Rep.KnownGaps, s)
	}
}

// Emitted counts one emitted event and widens the coverage.
func (a *Acc) Emitted(t time.Time) {
	a.Rep.Emitted++
	if a.Rep.First.IsZero() || t.Before(a.Rep.First) {
		a.Rep.First = t
	}
	if t.After(a.Rep.Last) {
		a.Rep.Last = t
	}
}

// Finish sorts hosts, fills coverage into Quality and applies the reject-fraction
// rule. The report is returned alongside any error.
func (a *Acc) Finish() (importer.Report, error) {
	for h := range a.hosts {
		a.Rep.ObservedHosts = append(a.Rep.ObservedHosts, h)
	}
	sort.Strings(a.Rep.ObservedHosts)
	if !a.Rep.First.IsZero() {
		f, l := a.Rep.First, a.Rep.Last
		a.Rep.Quality.CoverageFirst, a.Rep.Quality.CoverageLast = &f, &l
	}
	if a.Rep.Rows > 0 && float64(a.Rep.Rejected)/float64(a.Rep.Rows) > a.Lim.MaxRejectFraction {
		return a.Rep, &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
	}
	return a.Rep, nil
}

// Fail is Finish for an aborted parse: the report so far plus err.
func (a *Acc) Fail(err error) (importer.Report, error) {
	rep, _ := a.Finish()
	return rep, err
}

// NewEvent builds an Event with its ID and the common fields filled.
func NewEvent(opts importer.Options, source string, line int64, ordinal int) evidence.Event {
	return evidence.Event{
		EvidenceID: evidence.EventID(opts.CaseID, source, opts.ImportNonce, line, ordinal),
		Source:     source,
		Line:       line,
	}
}
