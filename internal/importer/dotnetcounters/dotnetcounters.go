// Package dotnetcounters imports the files written by "dotnet-counters collect"
// in its CSV and JSON formats (EventCounters and Meters, .NET 6 to 10). Each
// reading of an allowlisted counter becomes one "metric" Event.
//
// Format, pinned from the public dotnet/diagnostics source (src/Tools/
// dotnet-counters/Exporters/CSVExporter.cs and JSONExporter.cs) as read on its
// main branch; no release tag was fetched, so the shapes below are what that
// branch showed and not a version-stamped fact:
//
//   - CSV: header "Timestamp,Provider,Counter Name,Counter Type,Mean/Increment";
//     one reading per line. The timestamp is the exporting machine's local time
//     formatted with its culture (DateTime.ToString(), no zone, possibly with
//     AM/PM, a comma after the date, or day-first dates). The counter name cell
//     holds "Name (unit)" and, for Meters, the tags as "[key=value;key=value]";
//     none of it is quoted, so a tag value may hold commas. The value uses the
//     invariant culture.
//   - JSON: one document on one line,
//     {"TargetProcess":..,"StartTime":..,"Events":[{"timestamp","provider",
//     "name","tags","counterType","meterTags","instrumentTags","value"}]}. The
//     timestamp is round-trip ISO 8601 with the machine's offset ("Z" is read as
//     UTC); a timestamp with no offset is read as host-local time.
//
// Time: the CSV has no zone, so the host zone the user picked applies
// (RuleHostLocal) and a missing zone refuses the file. A reading inside a DST
// fold or gap is rejected (dst-ambiguous, dst-nonexistent). JSON readings with an
// offset are RuleOffsetInField, with "Z" RuleUTCField. Day and month order of a
// CSV date that has both leading fields at 12 or less cannot be known; see
// GapDateOrder.
//
// Privacy: the process name (TargetProcess), start time, route and address tags,
// meter and instrument tags, and every counter, provider, tag or value not on
// the fixed lists in counters.go are dropped at parse. Nothing from the file
// reaches an error, a report or a known gap. The process id is the owner's
// "pid" setting, never read from the file.
//
// A reading with a counter, provider or unit that is not on a list is dropped
// and counted as a row that was not emitted, not as a rejected row: a file of
// counters this importer does not know is complete, not damaged.
package dotnetcounters

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the importer name.
const Name = "dotnet-counters"

// SuppliedPID is the Options.Supplied key that states the process id the file
// was collected from ("dotnet-counters collect -p <pid>"). The file does not hold
// it. Without a numeric value no "pid" attribute is set.
const SuppliedPID = "pid"

// Fixed known-gap sentences.
const (
	GapLocalTime = "csv timestamps are host-local with no zone in the file; the host time zone chosen by the user is applied"
	GapDateOrder = "csv dates with the year last and both leading fields at 12 or less are read month/day/year when separated by slashes and day/month/year otherwise; a file written the other way has days 1 to 12 read with day and month swapped"
	GapDateMixed = "a csv date with a day above 12 came after rows read under the default date order; those earlier rows may have day and month swapped"
	GapCPU       = "CPU Usage is read as the share of all logical processors used by the process; the exporter's definition was not verified"
	GapCounters  = "readings of counters or providers that are not on the fixed list were dropped"
	GapUnits     = "readings with a unit that is not recognised were dropped"
	GapTags      = "tags other than percentile, generation, method, status and error type are dropped; readings that differ only in a dropped tag share one series"
	GapRates     = "rate counters are stored per second, with the value divided by the interval named in the counter; a byte or count rate has the unit per-sec and the counter name says which"
	GapNoValue   = "readings with no numeric value were dropped"
	GapNoPID     = "the process id was not supplied; readings are not tied to a process id"
	GapTruncated = "the json file ends inside the document; readings after that point are missing"
	GapWindow    = "readings outside the chosen time window were dropped"
)

// New returns the dotnet-counters importer.
func New() importer.Importer { return &imp{} }

type imp struct{}

func (*imp) Name() string { return Name }

// csvHeader is the exporter's header; the last cell is compared loosely.
var csvHeader = []string{"timestamp", "provider", "counter name", "counter type"}

// Detect scores a CSV with the exporter's header at 95 and a JSON document that
// starts with the exporter's members at 90, else 0.
func (*imp) Detect(head []byte, _ string) int {
	s := strings.TrimSpace(wincsv.HeadText(head))
	if strings.HasPrefix(s, "{") {
		t := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(s)
		if strings.HasPrefix(t, `{"TargetProcess":`) && strings.Contains(t, `"Events":[`) {
			return 90
		}
		return 0
	}
	line := s
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		line = s[:i]
	}
	if headerOK(strings.Split(line, ",")) {
		return 95
	}
	return 0
}

func headerOK(cells []string) bool {
	if len(cells) != 5 {
		return false
	}
	for i, want := range csvHeader {
		if strings.ToLower(strings.TrimSpace(cells[i])) != want {
			return false
		}
	}
	return strings.TrimSpace(cells[4]) != ""
}

// AllowedAttrs is the attribute set of exportPolicy["dotnet-counters"].
func (*imp) AllowedAttrs() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"provider", "counter", "percentile", "generation", "method", "status", "error-type", "pid"} {
		out[k] = true
	}
	return out
}

func (*imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 512 << 20, MaxRows: 5_000_000, MaxEvents: 5_000_000,
		MaxLineBytes: wincsv.DefaultMaxLineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// run is the state of one Parse call.
type run struct {
	opts    importer.Options
	acc     *wincsv.Acc
	emit    func(evidence.Event) error
	host    importer.HostHint
	pid     string
	window  bool
	ordinal int
}

func (m *imp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(Name, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	br := bufio.NewReaderSize(pr, 64<<10)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return acc.Fail(err)
	}
	if len(head) == 0 {
		return acc.Fail(textio.ErrEmpty)
	}
	// The readings carry no host name: they belong to the host the user picked.
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	rn := &run{opts: opts, acc: acc, emit: emit, host: *opts.Host,
		window: !opts.Window.From.IsZero() || !opts.Window.To.IsZero()}
	if pid := strings.TrimSpace(opts.Supplied[SuppliedPID]); pidValue.MatchString(pid) {
		rn.pid = pid
	} else {
		acc.Gap(GapNoPID)
	}
	acc.Rep.Quality.SourceVersion = opts.Host.Runtime
	if strings.HasPrefix(strings.TrimSpace(wincsv.HeadText(head)), "{") {
		err = rn.parseJSON(ctx, br, lim)
	} else {
		err = rn.parseCSV(ctx, br, lim)
	}
	if err != nil {
		return acc.Fail(err)
	}
	return acc.Finish()
}

// reading is one normalised sample with the fields every event needs.
type reading struct {
	line  int64
	utc   time.Time
	rule  string
	s     sample
	value string // the value cell as text
}

// handle normalises one reading and emits its event. It returns an error only
// when the whole file is to be refused or emit failed.
func (rn *run) handle(rd reading) error {
	n, why := normalise(rd.s)
	switch why {
	case dropProvider, dropCounter:
		rn.acc.Gap(GapCounters)
		return nil
	case dropUnit:
		rn.acc.Gap(GapUnits)
		return nil
	}
	v, ok, blank := numberCell(rd.value)
	if !ok {
		rn.acc.Reject(rd.line, importer.ReasonBadNumber)
		return nil
	}
	if blank {
		rn.acc.Gap(GapNoValue)
		return nil
	}
	if rn.window && !rn.opts.Window.Contains(rd.utc) {
		rn.acc.Gap(GapWindow)
		return nil
	}
	if n.interval > 0 {
		v /= n.interval
		rn.acc.Gap(GapRates)
	}
	v *= n.scale
	if n.counter == counterCPU && n.unit == unitHost {
		rn.acc.Gap(GapCPU)
	}
	if len(n.attrs) > 0 && hasTags(rd.s) {
		rn.acc.Gap(GapTags)
	}
	if rn.pid != "" {
		n.attrs["pid"] = rn.pid
	}
	if e := rn.acc.CheckEmit(1); e != nil {
		return e
	}
	rn.ordinal++
	vv := v
	ev := wincsv.NewEvent(rn.opts, Name, rd.line, rn.ordinal)
	ev.Kind, ev.Host, ev.Start, ev.End = "metric", rn.host.Label, rd.utc, rd.utc
	ev.Value, ev.Unit, ev.TZRule, ev.Attrs = &vv, n.unit, rd.rule, n.attrs
	if err := rn.emit(ev); err != nil {
		return err
	}
	rn.acc.Emitted(ev.Start)
	return nil
}

// hasTags reports whether the sample carried tags, which are dropped except for
// the allowlisted ones.
func hasTags(s sample) bool {
	if strings.TrimSpace(s.tags) != "" {
		return true
	}
	return strings.Contains(s.name, "[")
}

// ---- CSV -------------------------------------------------------------------

func (rn *run) parseCSV(ctx context.Context, br *bufio.Reader, lim importer.Limits) error {
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return err
	}
	// Header: the first line that is not blank.
	for {
		line, _, err := lr.Next()
		if err == io.EOF {
			return textio.ErrEmpty
		}
		var re importer.RowError
		if errors.As(err, &re) { // an unreadable header line is not a header
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		if !headerOK(strings.Split(string(line), ",")) {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		break
	}
	loc := rn.host.TZ
	if loc == nil {
		return wincsv.TZUnlabelled()
	}
	rn.acc.Rep.Quality.TimezoneRule = clock.RuleHostLocal
	rn.acc.Rep.FieldRules["timestamp"] = clock.RuleHostLocal
	rn.acc.Gap(GapLocalTime)
	rn.acc.Gap(GapDateOrder)
	var st cultureState
	defer func() {
		if st.mixed {
			rn.acc.Gap(GapDateMixed)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, no, err := lr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return err
			}
			if e := rn.acc.Row(no); e != nil {
				return e
			}
			rn.acc.Reject(no, reason)
			continue
		}
		text := string(line)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if e := rn.acc.Row(no); e != nil {
			return e
		}
		f, ok := splitCSVLine(text)
		if !ok {
			rn.acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		utc, reason, ok := parseCSVTime(f[0], loc, &st)
		if !ok {
			rn.acc.Reject(no, reason)
			continue
		}
		if !wincsv.SaneTime(utc) {
			rn.acc.Reject(no, importer.ReasonBadTime)
			continue
		}
		rd := reading{line: no, utc: utc, rule: clock.RuleHostLocal,
			s: sample{provider: f[1], name: f[2], ctype: f[3]}, value: f[4]}
		if err := rn.handle(rd); err != nil {
			return err
		}
	}
}

// splitCSVLine cuts a reading into timestamp, provider, counter name, counter
// type and value. The exporter does not quote, so the line is cut from both ends:
// the value and the type are after the last two commas, the timestamp is matched
// by its shape (it may hold a comma after the date), and the provider is the cell
// after it. What remains in the middle is the counter name, which may hold commas
// inside its tags. A line with a double quote is first tried as quoted CSV, for a
// file that a spreadsheet saved again.
func splitCSVLine(s string) ([5]string, bool) {
	if strings.IndexByte(s, '"') >= 0 {
		rd := csv.NewReader(strings.NewReader(s))
		rd.LazyQuotes, rd.FieldsPerRecord = true, -1
		if rec, err := rd.Read(); err == nil && len(rec) == 5 {
			return [5]string{rec[0], rec[1], rec[2], rec[3], rec[4]}, true
		}
	}
	var out [5]string
	i := strings.LastIndexByte(s, ',')
	if i < 0 {
		return out, false
	}
	out[4], s = s[i+1:], s[:i]
	i = strings.LastIndexByte(s, ',')
	if i < 0 {
		return out, false
	}
	out[3], s = s[i+1:], s[:i]
	// The timestamp ends at the first comma that follows its time of day.
	loc := timeEnd.FindStringIndex(s)
	if loc == nil {
		return out, false
	}
	out[0], s = s[:loc[1]], s[loc[1]:]
	s = strings.TrimPrefix(s, ",")
	i = strings.IndexByte(s, ',')
	if i < 0 {
		return out, false
	}
	out[1], out[2] = s[:i], s[i+1:]
	return out, true
}

// ---- JSON ------------------------------------------------------------------

type jsonReading struct {
	Timestamp   string          `json:"timestamp"`
	Provider    string          `json:"provider"`
	Name        string          `json:"name"`
	Tags        string          `json:"tags"`
	CounterType string          `json:"counterType"`
	Value       json.RawMessage `json:"value"`
}

func (rn *run) parseJSON(ctx context.Context, br *bufio.Reader, lim importer.Limits) error {
	dec := transform.NewReader(br, unicode.BOMOverride(unicode.UTF8.NewDecoder()))
	sc := &scanner{br: bufio.NewReaderSize(dec, 64<<10), max: lim.MaxLineBytes}
	b, err := sc.skipSpace()
	if err != nil || b != '{' {
		return jsonStart(err)
	}
	loc := rn.host.TZ
	sawEvents := false
	// A file that ends inside the Events array keeps its readings and says so; one
	// that ends before the array was reached holds nothing and is refused.
	truncated := func() error {
		if !sawEvents {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		rn.acc.Gap(GapTruncated)
		return nil
	}
	var st struct{ ruleSet bool }
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err = sc.skipSpace()
		if err != nil {
			return eofIs(err, truncated)
		}
		switch b {
		case '}':
			if !sawEvents {
				return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
			}
			return nil
		case ',':
			continue
		case '"':
		default:
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		key, err := sc.readString(64)
		if err != nil {
			return eofIs(err, truncated)
		}
		if b, err = sc.skipSpace(); err != nil || b != ':' {
			return eofIs(jsonStart(err), truncated)
		}
		if b, err = sc.skipSpace(); err != nil {
			return eofIs(err, truncated)
		}
		if !strings.EqualFold(key, "Events") || b != '[' {
			if _, err := sc.value(b, false); err != nil { // TargetProcess, StartTime, anything else: skipped
				return eofIs(err, truncated)
			}
			continue
		}
		sawEvents = true
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			b, err = sc.skipSpace()
			if err != nil {
				return eofIs(err, truncated)
			}
			if b == ']' {
				break
			}
			if b == ',' {
				continue
			}
			line := sc.line + 1
			raw, err := sc.value(b, true)
			if err != nil {
				return eofIs(err, truncated)
			}
			if e := rn.acc.Row(line); e != nil {
				return e
			}
			var jr jsonReading
			if b != '{' || json.Unmarshal(raw, &jr) != nil {
				rn.acc.Reject(line, importer.ReasonUnsupportedFormat)
				continue
			}
			utc, rule, reason, needLoc, ok := parseJSONTime(jr.Timestamp, loc)
			if needLoc {
				return wincsv.TZUnlabelled()
			}
			if !ok {
				rn.acc.Reject(line, reason)
				continue
			}
			if !wincsv.SaneTime(utc) {
				rn.acc.Reject(line, importer.ReasonBadTime)
				continue
			}
			if !st.ruleSet {
				st.ruleSet = true
				rn.acc.Rep.Quality.TimezoneRule = rule
				rn.acc.Rep.FieldRules["timestamp"] = rule
			}
			if rule == clock.RuleHostLocal {
				rn.acc.Gap(GapLocalTime)
			}
			rd := reading{line: line, utc: utc, rule: rule,
				s:     sample{provider: jr.Provider, name: jr.Name, tags: jr.Tags, ctype: jr.CounterType},
				value: jsonNumber(jr.Value)}
			if err := rn.handle(rd); err != nil {
				return err
			}
		}
	}
}

// jsonNumber gives the text of a JSON value cell: a number as written, a string
// without its quotes, null as empty.
func jsonNumber(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
		return "x"
	}
	return s
}

// jsonStart turns "not a JSON document" into the whole-file refusal, and passes a
// read error or a limit breach on unchanged.
func jsonStart(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	return err
}

// eofIs ends the parse quietly, with a gap, when the input stops inside the
// document; any other error is returned as it is.
func eofIs(err error, truncated func() error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return truncated()
	}
	return err
}
