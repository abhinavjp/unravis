// Package appinsights imports an Application Insights KQL result exported as
// CSV (plan card P9). It is an offline file importer: no network, no query
// execution.
//
// Documented query. The export must come from a query that projects, at most,
// these columns and uses the Logs "timestamp [UTC]" display:
//
//	union requests, dependencies
//	| project timestamp, itemType, name, duration, resultCode, success,
//	          itemCount, operation_Id, cloud_RoleInstance, type
//
// Required: `timestamp [UTC]` (ISO 8601, UTC). Kept: itemType, name,
// duration, resultCode, success, itemCount, operation_Id, cloud_RoleInstance
// (the host) and `type` (dependency type). Everything else is dropped at parse
// and never reaches an event, a report or an error: data (command text), url
// and its query, customDimensions, client_IP, user_Id and any unknown column.
//
// Labels. Rows are Symptom evidence only. Counts are sum(itemCount) and are
// estimates because of sampling. A missing record is never counter-evidence.
package appinsights

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// Name is the stable importer name.
const Name = "appinsights"

// Fixed sentences. KnownGaps and errors hold only these, never row content.
const (
	TimestampHeader = "timestamp [UTC]"
	// DurationLabel is how the duration column is shown to the analyst.
	DurationLabel = "attributed duration"
	// UseDocumentedQuery is appended to every header-level quarantine.
	UseDocumentedQuery = "use the documented query"

	GapSampled        = "sampled; counts are estimates"
	GapTimestampStart = "timestamp treated as operation start; not verified against the source"
	GapDuration       = "duration is the attributed duration recorded by the telemetry SDK, not time spent on the host or in a dependency"
	GapMissingRecord  = "a missing record is not evidence that the operation did not occur"
	GapNoItemCount    = "itemCount column absent; each row counted once"
	GapNoHost         = "cloud_RoleInstance column absent; host comes from the host picked for the file"
	GapEmpty          = "empty result; possibly no access"
)

// timestampIsStart records the unverified assumption that the Logs timestamp
// of a request or dependency is when it began. Flip it to treat timestamp as
// the end (Start = timestamp - duration) if the owner shows otherwise.
const timestampIsStart = true

const (
	maxItemCount  = 1_000_000_000
	maxDurationMs = 365 * 24 * 3600 * 1000.0
	maxNameBytes  = 256
)

var attrAllow = map[string]bool{
	"name": true, "dependencyType": true, "resultCode": true, "success": true, "operation_Id": true,
}

// kindFor maps an itemType (lower case) to the Event Kind enum. Anything else
// becomes "other"; raw itemType text never reaches an event.
var kindFor = map[string]string{
	"request": "request", "dependency": "dependency", "exception": "exception",
	"trace": "trace", "pageview": "pageview", "availabilityresult": "availability",
	"customevent": "custom-event",
}

// keepName lists kinds whose `name` column is an operation name. For the
// other kinds (exception, trace, ...) a name-like column can hold message text.
var keepName = map[string]bool{
	"request": true, "dependency": true, "pageview": true, "availability": true, "custom-event": true,
}

// knownHeaders maps a lower-case header to its canonical spelling. Only these
// are ever echoed in a Quarantine Column.
var knownHeaders = map[string]string{
	"timestamp [utc]": TimestampHeader, "itemtype": "itemType", "name": "name", "duration": "duration",
	"resultcode": "resultCode", "success": "success", "itemcount": "itemCount",
	"operation_id": "operation_Id", "cloud_roleinstance": "cloud_RoleInstance", "type": "type",
	"dependencytype": "dependencyType",
	// dropped columns, still known so a duplicate names them
	"data": "data", "url": "url", "customdimensions": "customDimensions",
	"client_ip": "client_IP", "user_id": "user_Id", "operation_name": "operation_Name",
}

// Importer implements importer.Importer.
type Importer struct{}

// New returns the App Insights importer.
func New() *Importer { return &Importer{} }

var _ importer.Importer = (*Importer)(nil)

func (*Importer) Name() string { return Name }

func (*Importer) AllowedAttrs() map[string]bool {
	m := make(map[string]bool, len(attrAllow))
	for k := range attrAllow {
		m[k] = true
	}
	return m
}

func (*Importer) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 512 << 20, MaxRows: 5_000_000, MaxLineBytes: 64 << 10,
		MaxFields: 4096, MaxRejectFraction: 0.05,
	}
}

// Detect scores the header line of the first 64 KiB.
func (*Importer) Detect(head []byte, fileName string) int {
	cells := headCells(head)
	if len(cells) == 0 {
		return 0
	}
	hasUTC, tsLike, aiCols := false, false, 0
	for _, c := range cells {
		k := strings.ToLower(strings.TrimSpace(c))
		switch k {
		case "itemtype", "operation_id", "itemcount", "cloud_roleinstance":
			aiCols++
		}
		if k == "timestamp [utc]" {
			hasUTC = true
		} else if strings.HasPrefix(k, "timestamp") && k != "timestamp_utc" {
			tsLike = true
		}
	}
	switch {
	case hasUTC && aiCols > 0:
		return 90
	case hasUTC:
		return 40
	case tsLike && aiCols >= 2:
		return 30 // ours, but not the documented query: Parse explains
	}
	return 0
}

// headCells returns the header cells of the first line, decoding a UTF-16 BOM.
func headCells(head []byte) []string {
	if len(head) >= 2 && ((head[0] == 0xFF && head[1] == 0xFE) || (head[0] == 0xFE && head[1] == 0xFF)) {
		end := len(head) &^ 1
		order := unicode.LittleEndian
		if head[0] == 0xFE {
			order = unicode.BigEndian
		}
		dec, err := unicode.UTF16(order, unicode.UseBOM).NewDecoder().Bytes(head[:end])
		if err != nil {
			return nil
		}
		head = dec
	}
	head = []byte(strings.TrimPrefix(string(head), "\xef\xbb\xbf"))
	if i := strings.IndexByte(string(head), '\n'); i >= 0 {
		head = head[:i]
	}
	line := strings.TrimRight(string(head), "\r")
	if line == "" || !utf8.ValidString(line) {
		return nil
	}
	cr := csv.NewReader(strings.NewReader(line))
	cr.FieldsPerRecord = -1
	cells, err := cr.Read()
	if err != nil {
		return nil
	}
	return cells
}

// queryError is a header-level quarantine that tells the owner what to do.
type queryError struct{ q *importer.QuarantineError }

func (e *queryError) Error() string { return e.q.Error() + ": " + UseDocumentedQuery }
func (e *queryError) Unwrap() error { return e.q }

func quarantine(reason importer.Reason, column string) error {
	return &queryError{&importer.QuarantineError{Reason: reason, Column: column}}
}

// Parse reads the CSV and emits one Event per valid row. r is what
// importer.Prepare returned: byte-capped and already sniffed for archives.
func (im *Importer) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rep := importer.Report{Importer: Name}
	lim := fillLimits(opts.Limits, im.DefaultLimits())

	cr, _, err := textio.NewCSV(r, textio.CSVOptions{Comma: ',', MaxFields: lim.MaxFields, MaxLine: lim.MaxLineBytes})
	if err != nil {
		return rep, err
	}
	cols, err := analyseHeader(cr.Header())
	if err != nil {
		return rep, err
	}

	rep.Rule(TimestampHeader, clock.RuleUTCField)
	for _, g := range []string{GapSampled, GapTimestampStart, GapDuration, GapMissingRecord} {
		rep.Gap(g)
	}
	if cols.itemCount < 0 {
		rep.Gap(GapNoItemCount)
	}
	if cols.host < 0 {
		rep.Gap(GapNoHost)
	}

	pickedHost := ""
	if opts.Host != nil {
		pickedHost = opts.Host.Label
	}
	hostCache := map[string]string{}
	var timeRejects int64

	reject := func(line int64, reason importer.Reason) {
		rep.Reject(line, reason)
		if reason == importer.ReasonBadTime {
			timeRejects++
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rec, line, err := cr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var re importer.RowError
			if errors.As(err, &re) {
				rep.Rows++
				if cerr := lim.CheckRows(rep.Rows); cerr != nil {
					return rep, cerr
				}
				reject(re.Line, re.Reason)
				continue
			}
			return rep, err // limit, read and context errors pass through unchanged
		}
		rep.Rows++
		if cerr := lim.CheckRows(rep.Rows); cerr != nil {
			return rep, cerr
		}

		ts, ok := parseUTC(rec[cols.ts])
		if !ok {
			reject(line, importer.ReasonBadTime)
			continue
		}
		dur, ok := parseDuration(cell(rec, cols.duration))
		if !ok {
			reject(line, importer.ReasonBadNumber)
			continue
		}
		count, ok := parseCount(cell(rec, cols.itemCount), cols.itemCount >= 0)
		if !ok {
			reject(line, importer.ReasonBadNumber)
			continue
		}

		kind := "record"
		if cols.itemType >= 0 {
			kind = kindFor[strings.ToLower(strings.TrimSpace(rec[cols.itemType]))]
			if kind == "" {
				kind = "other"
			}
		}

		host := pickedHost
		if cols.host >= 0 {
			if raw := strings.TrimSpace(rec[cols.host]); raw != "" {
				label, seen := hostCache[raw]
				if !seen {
					if oerr := rep.Observe(raw); oerr != nil {
						return rep, oerr
					}
					label = pickedHost
					if opts.Hosts != nil {
						h, found := opts.Hosts(raw)
						if !found {
							return rep, importer.ErrUnknownHost
						}
						label = h.Label
					}
					hostCache[raw] = label
				}
				host = label
			}
		}

		start, end := ts, ts
		if dur != nil {
			d := time.Duration(*dur * float64(time.Millisecond))
			if timestampIsStart {
				end = ts.Add(d)
			} else {
				start = ts.Add(-d)
			}
		}

		attrs := map[string]string{}
		if keepName[kind] {
			if n := sanitizeName(cell(rec, cols.name)); n != "" {
				attrs["name"] = n
			}
		}
		if kind == "dependency" {
			if t := cell(rec, cols.depType); t != "" {
				if validTypeName(t) {
					attrs["dependencyType"] = t
				} else {
					attrs["dependencyType"] = "other"
				}
			}
		}
		if rc := cell(rec, cols.resultCode); rc != "" {
			if len(rc) <= 10 && isNumeric(rc) { // HTTP status or numeric provider code; words become "other"
				attrs["resultCode"] = rc
			} else {
				attrs["resultCode"] = "other"
			}
		}
		switch strings.ToLower(strings.TrimSpace(cell(rec, cols.success))) {
		case "":
		case "true", "1":
			attrs["success"] = "true"
		case "false", "0":
			attrs["success"] = "false"
		default:
			attrs["success"] = "unknown"
		}
		if op := cell(rec, cols.opID); op != "" && validToken(op, 128) {
			attrs["operation_Id"] = op
		}
		if len(attrs) == 0 {
			attrs = nil
		}

		v := float64(count)
		ev := evidence.Event{
			EvidenceID: evidence.EventID(opts.CaseID, Name, opts.ImportNonce, line, 0),
			Source:     Name, Kind: kind, Host: host,
			Start: start.UTC(), End: end.UTC(), DurationMs: dur,
			Value: &v, Unit: "count", Line: line, TZRule: clock.RuleUTCField, Attrs: attrs,
		}
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
		rep.Cover(ev.Start)
	}

	if rep.Rows == 0 {
		rep.Gap(evidence.NotObservedGap(GapEmpty))
	}
	if cerr := lim.CheckRejects(rep.Rows, rep.Rejected); cerr != nil {
		if timeRejects*2 > rep.Rejected {
			return rep, &queryError{&importer.QuarantineError{Reason: importer.ReasonRejectFraction}}
		}
		return rep, cerr
	}

	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
	}
	rep.Quality.Sampling = GapSampled
	rep.Quality.TimezoneRule = clock.RuleUTCField
	return rep, nil
}

type columns struct {
	ts, itemType, name, duration, resultCode, success, itemCount, opID, host, depType int
}

func analyseHeader(h []string) (columns, error) {
	c := columns{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
	idx := map[string]int{}
	tsLike := false
	for i, raw := range h {
		k := strings.ToLower(strings.TrimSpace(raw))
		if k == "" {
			continue
		}
		if strings.HasPrefix(k, "timestamp") && k != "timestamp [utc]" {
			tsLike = true
		}
		if _, dup := idx[k]; dup {
			return c, quarantine(importer.ReasonUnsupportedFormat, knownHeaders[k]) // "" when unknown
		}
		idx[k] = i
	}
	ts, ok := idx["timestamp [utc]"]
	if !ok {
		if tsLike {
			return c, quarantine(importer.ReasonTZUnlabelled, "timestamp")
		}
		return c, quarantine(importer.ReasonUnsupportedFormat, TimestampHeader)
	}
	get := func(names ...string) int {
		for _, n := range names {
			if i, ok := idx[n]; ok {
				return i
			}
		}
		return -1
	}
	c.ts = ts
	c.itemType = get("itemtype")
	c.name = get("name")
	c.duration = get("duration")
	c.resultCode = get("resultcode")
	c.success = get("success")
	c.itemCount = get("itemcount")
	c.opID = get("operation_id")
	c.host = get("cloud_roleinstance")
	c.depType = get("type", "dependencytype")
	return c, nil
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

func fillLimits(l, d importer.Limits) importer.Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxRows <= 0 {
		l.MaxRows = d.MaxRows
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = d.MaxLineBytes
	}
	if l.MaxFields <= 0 {
		l.MaxFields = d.MaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = d.MaxRejectFraction
	}
	return l.WithDefaults()
}

// parseUTC accepts ISO 8601 with a Z or +00:00 zone and nothing else: the
// header promises UTC, so any other offset or a locale format is a bad row.
func parseUTC(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	if _, off := t.Zone(); off != 0 {
		return time.Time{}, false
	}
	t = t.UTC()
	if y := t.Year(); y < 1970 || y > 2200 {
		return time.Time{}, false
	}
	return t, true
}

// parseDuration: empty or "-" is no duration (nil); otherwise milliseconds.
func parseDuration(s string) (*float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return nil, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > maxDurationMs {
		return nil, false
	}
	return &f, true
}

// parseCount reads itemCount. An absent column counts each row once; a
// present cell must be a whole number of at least 1.
func parseCount(s string, present bool) (int64, bool) {
	if !present {
		return 1, true
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 1 || n > maxItemCount {
		return 0, false
	}
	return n, true
}

// validTypeName allows dependency type names such as "SQL", "Http" and
// "Azure Service Bus": letters, digits, space, parentheses, _ . -, up to 64 bytes.
func validTypeName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '(' || s[i] == ')' {
			continue
		}
		if !validToken(s[i:i+1], 1) {
			return false
		}
	}
	return true
}

func validToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

// sanitizeName drops any query string, turns GUID and numeric path segments
// into {id}, and caps the result at 256 bytes. Other characters are kept
// verbatim; CSV escaping is the exporter's job.
func sanitizeName(s string) string {
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	segs := strings.Split(s, "/")
	for i, seg := range segs {
		if isNumeric(seg) || isGUID(seg) {
			segs[i] = "{id}"
		}
	}
	s = strings.Join(segs, "/")
	if len(s) > maxNameBytes {
		s = s[:maxNameBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isGUID(s string) bool {
	switch len(s) {
	case 32:
		return isHex(s)
	case 36:
		return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-' &&
			isHex(s[:8]) && isHex(s[9:13]) && isHex(s[14:18]) && isHex(s[19:23]) && isHex(s[24:])
	}
	return false
}

// EstimatedCount is sum(itemCount) over events: an estimate, because the
// source is sampled.
func EstimatedCount(ev []evidence.Event) int64 {
	var n int64
	for _, e := range ev {
		if e.Value != nil {
			n += int64(*e.Value)
		}
	}
	return n
}

// Absence words an empty finding for a window. It never reads as counter
// evidence: no rows is either no access or sampling, not "did not happen".
func Absence(rowsInWindow int64) string {
	if rowsInWindow == 0 {
		return evidence.NotObservedGap(GapEmpty)
	}
	return evidence.NotObservedGap(GapSampled + "; a missing record is not counter-evidence")
}
