// Package wincsv holds what the two Windows CSV importers (wincounters and
// winevents) share, so P3's PDH importer can reuse it through wincounters:
// limit merging, input preparation (byte cap, archive and binary refusal), CSV
// opening on top of textio, and report accumulation. BOM decoding, line and
// record guards and strict CSV reading are textio's. Nothing here reads a cell
// into an error or a log.
//
// Exported API (kept stable; P3 and later cards may rely on it):
//
//	MergeLimits, Prepare, OpenCSV, RowReason, TZUnlabelled, MissingColumn,
//	HeadText, ClipAttr, Acc (NewAcc, Row, Reject, Observe, Gap, Emitted, Finish,
//	Fail), NewEvent, and the Default* constants.
package wincsv

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"sort"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// Defaults applied when a Limits field is zero (plan section 4).
const (
	DefaultMaxLineBytes = importer.DefaultMaxLineBytes
	DefaultMaxFields    = importer.DefaultMaxFields
	DefaultRejectFrac   = importer.DefaultMaxRejectFraction
	// MaxObservedHosts bounds Report.ObservedHosts.
	MaxObservedHosts = importer.MaxObservedHosts
	maxAttrBytes     = 256
)

// evtxMagic is the Windows event log (.evtx) file signature. A .blg performance
// log has no text header; binary input of any kind is caught by the NUL check.
var evtxMagic = []byte("ElfFile\x00")

// MergeLimits fills zero fields of l from def, then from the section 4 defaults.
// A negative MaxBytes or MaxRows is kept: it means no limit.
func MergeLimits(l, def importer.Limits) importer.Limits {
	if l.MaxBytes == 0 {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxRows == 0 {
		l.MaxRows = def.MaxRows
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = def.MaxLineBytes
	}
	if l.MaxFields <= 0 {
		l.MaxFields = def.MaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = def.MaxRejectFraction
	}
	return l.WithDefaults()
}

// Prepare applies the byte cap and refuses archives (importer.Sniff), .evtx files
// and other binary input (a NUL in the head of a file that does not start with a
// UTF-16 BOM) as unsupported-format. The returned reader still yields every byte.
// lim must already be merged. Read errors come back unchanged.
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
	return br, nil
}

// OpenCSV is Prepare followed by textio.NewCSV (BOM decoding, strict CSV, line,
// record and field caps from lim). comment is the comment rune or 0. An empty or
// header-less input returns textio.ErrEmpty itself (never modify it).
func OpenCSV(r io.Reader, lim importer.Limits, comment rune) (*textio.CSVReader, error) {
	pr, err := Prepare(r, lim)
	if err != nil {
		return nil, err
	}
	cr, _, err := textio.NewCSV(pr, textio.CSVOptions{
		Comma: ',', Comment: comment, MaxFields: lim.MaxFields, MaxLine: lim.MaxLineBytes,
	})
	if err != nil {
		return nil, err
	}
	return cr, nil
}

// RowReason classifies a textio Next error that rejects one row rather than the file.
func RowReason(err error) (importer.Reason, bool) {
	var re importer.RowError
	if errors.As(err, &re) {
		return re.Reason, true
	}
	return "", false
}

// TZUnlabelled is the whole-file refusal for host-local times without a host zone.
// It matches both importer.ErrTimezoneUnlabelled and a *QuarantineError.
func TZUnlabelled() error {
	return errors.Join(&importer.QuarantineError{Reason: importer.ReasonTZUnlabelled}, importer.ErrTimezoneUnlabelled)
}

// MissingColumn refuses a file whose header lacks a required column. name must be
// one of the importer's own fixed column names, never text from the file. There is
// no dedicated Reason for it, so it reuses ReasonUnsupportedFormat, as textio does.
func MissingColumn(name string) error {
	return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: name}
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
	Rep importer.Report
	Lim importer.Limits
}

// NewAcc starts a report for importer name. lim must already be merged.
func NewAcc(name string, lim importer.Limits) *Acc {
	return &Acc{Rep: importer.Report{Importer: name, FieldRules: map[string]string{}}, Lim: lim}
}

// Row counts one data row and enforces the row cap.
func (a *Acc) Row(line int64) error {
	a.Rep.Rows++
	return a.Lim.CheckRows(a.Rep.Rows)
}

// Reject counts a rejected row with its reason.
func (a *Acc) Reject(line int64, reason importer.Reason) { a.Rep.Reject(line, reason) }

// Observe records a raw host name seen in the file (an empty name is ignored). It
// returns a *LimitError past MaxObservedHosts distinct names; the caller must fail.
func (a *Acc) Observe(host string) error {
	if host == "" {
		return nil
	}
	return a.Rep.Observe(host)
}

// Gap adds a fixed known-gap sentence once.
func (a *Acc) Gap(s string) { a.Rep.Gap(s) }

// Emitted counts one emitted event and widens the coverage.
func (a *Acc) Emitted(t time.Time) {
	a.Rep.Emitted++
	a.Rep.Cover(t)
}

// Finish sorts hosts, fills coverage into Quality and applies the reject-fraction
// rule. The report is returned alongside any error.
func (a *Acc) Finish() (importer.Report, error) {
	sort.Strings(a.Rep.ObservedHosts)
	if !a.Rep.First.IsZero() {
		f, l := a.Rep.First, a.Rep.Last
		a.Rep.Quality.CoverageFirst, a.Rep.Quality.CoverageLast = &f, &l
	}
	return a.Rep, a.Lim.CheckRejects(a.Rep.Rows, a.Rep.Rejected)
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
