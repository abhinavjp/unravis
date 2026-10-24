// Package importertest is the conformance harness every importer must pass
// (plan §1.6). An importer's test calls Run with a good and a bad fixture, and
// its fuzz target calls FuzzParse:
//
//	func TestConformance(t *testing.T) {
//		importertest.Run(t, New(), importertest.Fixtures{Good: good, Bad: bad})
//	}
//
//	func FuzzParse(f *testing.F) {
//		importertest.FuzzParse(f, New(), importertest.Fixtures{Good: good, Bad: bad})
//	}
//
// Fixtures must be synthetic: 2001 dates, RFC 5737 addresses, *.example.test
// hosts and invented names (plan §4).
package importertest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Sentinel is planted in a bad cell of Fixtures.Bad. It must never appear in
// any error, report, known gap or event attribute.
const Sentinel = "SENTINEL-7f3a"

// Fixtures are the synthetic files an importer is exercised with.
type Fixtures struct {
	// Good is a valid UTF-8 file that yields events, has at least two rows
	// (so a row limit of one is a breach) and at least one line longer than
	// one byte. It may start with a UTF-8 BOM; the harness strips it first.
	Good []byte
	// Bad is a file with at least one rejectable row whose bad cell contains
	// Sentinel. The importer may reject the row or refuse the whole file.
	Bad []byte
	// Formula is optional: a valid file in which at least one free-text
	// cell that lands in an event attribute starts with one of = + - @ tab
	// or CR. Leave nil for importers whose events carry no free text.
	Formula []byte
	// Options is the base Options (Host, Hosts, Supplied and so on). CaseID,
	// ImportNonce, FileName and Pseudonymize are filled in when empty.
	Options importer.Options
}

func (fx Fixtures) opts() importer.Options {
	o := fx.Options
	if o.CaseID == "" {
		o.CaseID = "harnesscase00000000000000a"
	}
	if o.ImportNonce == "" {
		o.ImportNonce = "harness-nonce-1"
	}
	if o.FileName == "" {
		o.FileName = "fixture.txt"
	}
	if o.Pseudonymize == nil {
		o.Pseudonymize = func(kind, value string) string { return kind + "-0000" }
	}
	return o
}

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}
	zipHead = []byte("PK\x03\x04\x14\x00\x00\x00")
	gzHead  = []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}
)

func stripBOM(b []byte) []byte { return bytes.TrimPrefix(b, utf8BOM) }

func utf16LE(t testing.TB, b []byte) []byte {
	t.Helper()
	out, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder(), b)
	if err != nil {
		t.Fatalf("encoding fixture as UTF-16: %v", err)
	}
	return out
}

// safely runs f and turns a panic into a test failure.
func safely(t *testing.T, what string, f func()) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic: %v", what, r)
			ok = false
		}
	}()
	f()
	return true
}

func collect(imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return importer.Collect(ctx, imp, bytes.NewReader(data), o)
}

// Run executes the whole conformance list as subtests of t.
func Run(t *testing.T, imp importer.Importer, fx Fixtures) {
	t.Helper()
	if len(fx.Good) < 2 {
		t.Fatal("importertest: Fixtures.Good is empty or one byte")
	}
	if !bytes.Contains(fx.Bad, []byte(Sentinel)) {
		t.Fatalf("importertest: Fixtures.Bad must contain %s in a bad cell", Sentinel)
	}
	if bytes.Contains(fx.Good, []byte(Sentinel)) {
		t.Fatalf("importertest: Fixtures.Good must not contain %s", Sentinel)
	}
	fx.Good = stripBOM(fx.Good)

	t.Run("good-fixture", func(t *testing.T) { goodFixture(t, imp, fx) })
	t.Run("detect", func(t *testing.T) { detect(t, imp, fx) })
	t.Run("archives-refused", func(t *testing.T) { archives(t, imp, fx) })
	t.Run("limit-bytes", func(t *testing.T) { limitBytes(t, imp, fx) })
	t.Run("limit-rows", func(t *testing.T) { limitRows(t, imp, fx) })
	t.Run("limit-line", func(t *testing.T) { limitLine(t, imp, fx) })
	t.Run("encodings-identical", func(t *testing.T) { encodings(t, imp, fx) })
	t.Run("sentinel-never-leaks", func(t *testing.T) { sentinel(t, imp, fx) })
	t.Run("formula-cells-verbatim", func(t *testing.T) { formula(t, imp, fx) })
	t.Run("truncated-and-tiny-files", func(t *testing.T) { tiny(t, imp, fx) })
	t.Run("context-cancel", func(t *testing.T) { cancellation(t, imp, fx) })
}

func goodFixture(t *testing.T, imp importer.Importer, fx Fixtures) {
	evs, rep, err := collect(imp, fx.Good, fx.opts())
	if err != nil {
		t.Fatalf("Good fixture refused: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("Good fixture yielded no events")
	}
	allowed := imp.AllowedAttrs()
	ids := map[string]bool{}
	for i, ev := range evs {
		if err := ev.Validate(allowed); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if ev.Source != imp.Name() {
			t.Fatalf("event %d: source %q, want %q", i, ev.Source, imp.Name())
		}
		if ids[ev.EvidenceID] {
			t.Fatalf("event %d: duplicate evidence id", i)
		}
		ids[ev.EvidenceID] = true
	}
	if rep.Importer != imp.Name() {
		t.Errorf("report importer %q, want %q", rep.Importer, imp.Name())
	}
	checkReport(t, rep)
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("known gap reads as causal: %v", err)
		}
	}
	// Same input, same options: same output.
	evs2, rep2, err := collect(imp, fx.Good, fx.opts())
	if err != nil || !reflect.DeepEqual(evs, evs2) || !reflect.DeepEqual(rep, rep2) {
		t.Errorf("a second parse of the same bytes differs (err %v)", err)
	}
}

// checkReport holds the invariants every Report keeps, whatever the input.
func checkReport(t testing.TB, rep importer.Report) {
	t.Helper()
	if rep.Rows < 0 || rep.Emitted < 0 || rep.Rejected < 0 || rep.Rejected > rep.Rows {
		t.Errorf("report counts inconsistent: rows %d emitted %d rejected %d", rep.Rows, rep.Emitted, rep.Rejected)
	}
	if len(rep.Errors) > importer.MaxReportErrors {
		t.Errorf("report keeps %d row errors, max %d", len(rep.Errors), importer.MaxReportErrors)
	}
	for _, re := range rep.Errors {
		if !re.Reason.Valid() {
			t.Errorf("row error has a reason outside the fixed set")
		}
	}
	if len(rep.ObservedHosts) > importer.MaxObservedHosts {
		t.Errorf("report keeps %d observed hosts", len(rep.ObservedHosts))
	}
	if !rep.First.IsZero() || !rep.Last.IsZero() {
		if rep.First.Location() != time.UTC || rep.Last.Location() != time.UTC || rep.Last.Before(rep.First) {
			t.Errorf("report coverage is not an ordered UTC range")
		}
	}
}

func detect(t *testing.T, imp importer.Importer, fx Fixtures) {
	head := fx.Good
	if len(head) > importer.HeadSize {
		head = head[:importer.HeadSize]
	}
	for name, h := range map[string][]byte{
		"good": head, "nil": nil, "one-byte": {'x'}, "nul": {0}, "binary": bytes.Repeat([]byte{0xff, 0x00, 0x7f}, 100),
		"bad": fx.Bad, "zip": zipHead,
	} {
		var s int
		if !safely(t, "Detect("+name+")", func() { s = imp.Detect(h, "fixture.txt") }) {
			continue
		}
		if s < 0 || s > 100 {
			t.Errorf("Detect(%s) = %d, want 0..100", name, s)
		}
		if name == "good" && s == 0 {
			t.Errorf("Detect gives 0 for the importer's own Good fixture")
		}
	}
}

func archives(t *testing.T, imp importer.Importer, fx Fixtures) {
	for name, magic := range map[string][]byte{"zip": zipHead, "gzip": gzHead} {
		body := append(append([]byte(nil), magic...), fx.Good...)
		safely(t, name, func() {
			evs, _, err := collect(imp, body, fx.opts())
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) || qe.Reason != importer.ReasonArchive {
				t.Errorf("%s head: got %v, want a QuarantineError for an archive", name, err)
				return
			}
			if !strings.Contains(err.Error(), "extract first") {
				t.Errorf("%s head: message %q does not say to extract first", name, err)
			}
			if len(evs) != 0 {
				t.Errorf("%s head: %d events returned", name, len(evs))
			}
		})
	}
}

// parseCounting runs Prepare and Parse directly, counting emitted events, so
// a limit breach can be checked at the point Parse returns.
func parseCounting(imp importer.Importer, data []byte, o importer.Options) (emitted int, rep importer.Report, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, o, err := importer.Prepare(imp, bytes.NewReader(data), o)
	if err != nil {
		return 0, rep, err
	}
	rep, err = imp.Parse(ctx, body, o, func(evidence.Event) error { emitted++; return nil })
	return emitted, rep, err
}

func wantLimit(t *testing.T, what, limit string, err error) {
	t.Helper()
	var le *importer.LimitError
	if !errors.As(err, &le) {
		t.Errorf("%s: got %v, want a *LimitError", what, err)
		return
	}
	if limit != "" && le.Limit != limit {
		t.Errorf("%s: limit named %q, want %q", what, le.Limit, limit)
	}
}

func limitBytes(t *testing.T, imp importer.Importer, fx Fixtures) {
	o := fx.opts()
	o.Limits.MaxBytes = int64(len(fx.Good)) - 1
	evs, _, err := collect(imp, fx.Good, o)
	wantLimit(t, "one byte over MaxBytes", "bytes", err)
	if len(evs) != 0 {
		t.Errorf("%d events returned after a byte-limit breach", len(evs))
	}
	o.Limits.MaxBytes = int64(len(fx.Good))
	if _, _, err := collect(imp, fx.Good, o); err != nil {
		t.Errorf("exactly MaxBytes must pass: %v", err)
	}
}

func limitRows(t *testing.T, imp importer.Importer, fx Fixtures) {
	baseline, _, err := parseCounting(imp, fx.Good, fx.opts())
	if err != nil || baseline == 0 {
		t.Fatalf("baseline parse: %d events, %v", baseline, err)
	}
	o := fx.opts()
	o.Limits.MaxRows = 1
	var emitted int
	var err2 error
	safely(t, "parse with MaxRows=1", func() { emitted, _, err2 = parseCounting(imp, fx.Good, o) })
	wantLimit(t, "Parse with MaxRows=1 (the importer enforces rows itself)", "rows", err2)
	if emitted >= baseline {
		t.Errorf("parse emitted %d of %d events; nothing may be emitted after a breach", emitted, baseline)
	}
	evs, _, err := collect(imp, fx.Good, o)
	wantLimit(t, "Collect with MaxRows=1", "rows", err)
	if len(evs) != 0 {
		t.Errorf("%d events returned after a row-limit breach", len(evs))
	}
}

func longestLine(b []byte) int {
	longest := 0
	for _, l := range bytes.Split(b, []byte("\n")) {
		l = bytes.TrimSuffix(l, []byte("\r"))
		if len(l) > longest {
			longest = len(l)
		}
	}
	return longest
}

func limitLine(t *testing.T, imp importer.Importer, fx Fixtures) {
	l := longestLine(fx.Good)
	if l < 2 {
		t.Fatal("Good fixture has no line longer than one byte")
	}
	baseline, _, err := parseCounting(imp, fx.Good, fx.opts())
	if err != nil {
		t.Fatalf("baseline parse: %v", err)
	}
	o := fx.opts()
	o.Limits.MaxLineBytes = l - 1
	var emitted int
	var err2 error
	safely(t, "parse with a line limit", func() { emitted, _, err2 = parseCounting(imp, fx.Good, o) })
	wantLimit(t, fmt.Sprintf("a %d-byte line over MaxLineBytes %d", l, l-1), "line-bytes", err2)
	if emitted >= baseline {
		t.Errorf("parse emitted %d of %d events; nothing may be emitted after a breach", emitted, baseline)
	}
	evs, _, err := collect(imp, fx.Good, o)
	wantLimit(t, "Collect with a line limit", "line-bytes", err)
	if len(evs) != 0 {
		t.Errorf("%d events returned after a line-limit breach", len(evs))
	}
	o.Limits.MaxLineBytes = l
	if _, _, err := collect(imp, fx.Good, o); err != nil {
		t.Errorf("a line of exactly MaxLineBytes must pass: %v", err)
	}
}

func encodings(t *testing.T, imp importer.Importer, fx Fixtures) {
	want, wantRep, err := collect(imp, fx.Good, fx.opts())
	if err != nil {
		t.Fatalf("Good fixture refused: %v", err)
	}
	for name, data := range map[string][]byte{
		"UTF-8 BOM":    append(append([]byte(nil), utf8BOM...), fx.Good...),
		"UTF-16LE BOM": utf16LE(t, fx.Good),
	} {
		var got []evidence.Event
		var rep importer.Report
		var err error
		if !safely(t, name, func() { got, rep, err = collect(imp, data, fx.opts()) }) {
			continue
		}
		if err != nil {
			t.Errorf("%s copy refused: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s copy yields different events (%d vs %d)", name, len(got), len(want))
		}
		if rep.Rows != wantRep.Rows || rep.Emitted != wantRep.Emitted || rep.Rejected != wantRep.Rejected ||
			!reflect.DeepEqual(rep.FieldRules, wantRep.FieldRules) || !reflect.DeepEqual(rep.KnownGaps, wantRep.KnownGaps) {
			t.Errorf("%s copy yields a different report", name)
		}
	}
}

// exposed gathers every string a run exposes: the error chain, the report and
// the events.
func exposed(rep importer.Report, evs []evidence.Event, err error) []string {
	var out []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		out = append(out, e.Error(), fmt.Sprintf("%#v", e))
	}
	out = append(out, fmt.Sprintf("%#v", rep), fmt.Sprintf("%+v", rep))
	out = append(out, rep.KnownGaps...)
	out = append(out, rep.ObservedHosts...)
	for _, v := range rep.FieldRules {
		out = append(out, v)
	}
	for _, ev := range evs {
		out = append(out, fmt.Sprintf("%#v", ev))
		for k, v := range ev.Attrs {
			out = append(out, k, v)
		}
	}
	return out
}

func leaks(t testing.TB, what string, rep importer.Report, evs []evidence.Event, err error) {
	t.Helper()
	for _, s := range exposed(rep, evs, err) {
		if strings.Contains(s, Sentinel) {
			t.Errorf("%s: the sentinel appears in an error, report, gap or event", what)
			return
		}
	}
}

func sentinel(t *testing.T, imp importer.Importer, fx Fixtures) {
	// As the file arrives: usually a reject or a quarantine.
	var evs []evidence.Event
	var rep importer.Report
	var err error
	safely(t, "Bad fixture", func() { evs, rep, err = collect(imp, fx.Bad, fx.opts()) })
	leaks(t, "default limits", rep, evs, err)

	// With every reject tolerated, so rows and events are reported.
	o := fx.opts()
	o.Limits.MaxRejectFraction = 1
	safely(t, "Bad fixture, rejects tolerated", func() { evs, rep, err = collect(imp, fx.Bad, o) })
	leaks(t, "rejects tolerated", rep, evs, err)
	if err == nil && rep.Rejected == 0 {
		t.Errorf("Bad fixture is not bad: nothing was rejected and the file was accepted")
	}
	// The bad cell may also reach a limit message.
	o.Limits.MaxLineBytes = 8
	safely(t, "Bad fixture, tiny line limit", func() { evs, rep, err = collect(imp, fx.Bad, o) })
	leaks(t, "tiny line limit", rep, evs, err)
	o.Limits.MaxLineBytes = 0
	o.Limits.MaxRows = 1
	safely(t, "Bad fixture, one-row limit", func() { evs, rep, err = collect(imp, fx.Bad, o) })
	leaks(t, "one-row limit", rep, evs, err)
	// And through the other encoding.
	o = fx.opts()
	o.Limits.MaxRejectFraction = 1
	safely(t, "Bad fixture as UTF-16", func() { evs, rep, err = collect(imp, utf16LE(t, stripBOM(fx.Bad)), o) })
	leaks(t, "UTF-16", rep, evs, err)
}

var triggers = []string{"=", "+", "-", "@", "\t", "\r"}

func startsWithTrigger(s string) bool {
	for _, tr := range triggers {
		if strings.HasPrefix(s, tr) {
			return true
		}
	}
	return false
}

func formula(t *testing.T, imp importer.Importer, fx Fixtures) {
	if len(fx.Formula) == 0 {
		t.Skip("importer supplies no Formula fixture (no free-text cells in its events)")
	}
	evs, _, err := collect(imp, fx.Formula, fx.opts())
	if err != nil {
		t.Fatalf("Formula fixture refused: %v", err)
	}
	verbatim := 0
	for _, ev := range evs {
		for k, v := range ev.Attrs {
			if startsWithTrigger(v) {
				verbatim++
			}
			if strings.HasPrefix(v, "'") && startsWithTrigger(v[1:]) {
				t.Errorf("attribute %q was escaped; escaping belongs to export, not import", k)
			}
		}
	}
	if verbatim == 0 {
		t.Error("no attribute value starts with = + - @ tab or CR: the cell was dropped, altered or the fixture does not exercise it")
	}
}

func tiny(t *testing.T, imp importer.Importer, fx Fixtures) {
	type tc struct {
		name    string
		data    []byte
		mustErr bool
	}
	cases := []tc{
		{"empty", nil, true},
		{"empty-non-nil", []byte{}, true},
		{"one-byte", fx.Good[:1], true},
		{"one-newline", []byte("\n"), false},
		{"one-nul", []byte{0}, false},
		{"one-0xff", []byte{0xff}, false},
		{"lone-bom", utf8BOM, false},
		{"utf16-bom-only", []byte{0xff, 0xfe}, false},
		{"utf16-bom-odd", []byte{0xff, 0xfe, 'a'}, false},
		{"minus-last-byte", fx.Good[:len(fx.Good)-1], false},
		{"half", fx.Good[:len(fx.Good)/2], false},
	}
	step := 1
	if len(fx.Good) > 400 {
		step = len(fx.Good) / 200
	}
	for n := 1; n < len(fx.Good); n += step {
		cases = append(cases, tc{fmt.Sprintf("cut-%d", n), fx.Good[:n], false})
	}
	// Truncated UTF-16 as well.
	u := utf16LE(t, fx.Good)
	for _, n := range []int{3, 4, 5, len(u) / 2, len(u) - 1} {
		if n > 0 && n < len(u) {
			cases = append(cases, tc{fmt.Sprintf("utf16-cut-%d", n), u[:n], false})
		}
	}
	for _, c := range cases {
		var err error
		var evs []evidence.Event
		var rep importer.Report
		if !safely(t, c.name, func() { evs, rep, err = collect(imp, c.data, fx.opts()) }) {
			continue
		}
		if c.mustErr && err == nil {
			t.Errorf("%s: accepted without error", c.name)
		}
		if err != nil && err.Error() == "" {
			t.Errorf("%s: empty error message", c.name)
		}
		if err != nil && len(evs) != 0 {
			t.Errorf("%s: %d events returned with an error", c.name, len(evs))
		}
		checkReport(t, rep)
	}
}

// endless yields the same bytes forever, ending with EOF after a safety
// deadline so a parser that ignores its context cannot spin for ever.
type endless struct {
	chunk    []byte
	off      int
	deadline time.Time
}

func (e *endless) Read(p []byte) (int, error) {
	if time.Now().After(e.deadline) {
		return 0, io.EOF
	}
	if e.off >= len(e.chunk) {
		e.off = 0
	}
	n := copy(p, e.chunk[e.off:])
	e.off += n
	return n, nil
}

func cancellation(t *testing.T, imp importer.Importer, fx Fixtures) {
	chunk := append([]byte(nil), fx.Good...)
	if chunk[len(chunk)-1] != '\n' {
		chunk = append(chunk, '\n')
	}
	o := fx.opts()
	o.Limits.MaxBytes = 1 << 40
	o.Limits.MaxRows = 1 << 40
	o.Limits.MaxRejectFraction = 1

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	type result struct {
		err error
		n   int
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		body := &endless{chunk: chunk, deadline: time.Now().Add(10 * time.Second)}
		evs, _, err := importer.Collect(ctx, imp, body, o)
		done <- result{err: err, n: len(evs)}
	}()
	time.Sleep(50 * time.Millisecond)
	cancelled := time.Now()
	stop()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("parse of an endless input ended without error")
		}
		if strings.HasPrefix(r.err.Error(), "panic:") {
			t.Fatal(r.err)
		}
		if !errors.Is(r.err, context.Canceled) {
			// The importer may legitimately have refused the repeated input
			// (a header repeated in the middle of a file, a second XML
			// document); it stopped, which is what matters here.
			t.Logf("stopped with %v rather than the context error", r.err)
		}
		if r.n != 0 {
			t.Errorf("%d events returned with an error", r.n)
		}
		if d := time.Since(cancelled); d > time.Second {
			t.Errorf("parse took %v to stop after cancel, want under 1s", d)
		}
	case <-time.After(time.Second):
		t.Fatal("parse did not stop within 1s of context cancellation")
	}
}

// FuzzParse is the body of an importer's fuzz target. It seeds the corpus
// with the fixtures and checks that no input panics, hangs or leaks the
// sentinel, and that every accepted input yields valid events.
func FuzzParse(f *testing.F, imp importer.Importer, fx Fixtures) {
	good := stripBOM(fx.Good)
	for _, seed := range [][]byte{
		fx.Good, fx.Bad, fx.Formula,
		append(append([]byte(nil), utf8BOM...), good...),
		utf16LE(f, good),
		good[:len(good)/2],
		{}, {'x'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		o := fx.opts()
		o.Limits = importer.Limits{MaxBytes: 1 << 20, MaxRows: 20000, MaxRejectFraction: 1}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		evs, rep, err := importer.Collect(ctx, imp, bytes.NewReader(data), o)
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("parse did not finish within 10s")
		}
		if err != nil && len(evs) != 0 {
			t.Fatalf("%d events returned with an error", len(evs))
		}
		for i, ev := range evs {
			if verr := ev.Validate(imp.AllowedAttrs()); verr != nil {
				t.Fatalf("event %d: %v", i, verr)
			}
		}
		checkReport(t, rep)
		if bytes.Contains(data, []byte(Sentinel)) {
			leaks(t, "fuzz input", rep, evs, err)
		}
	})
}
