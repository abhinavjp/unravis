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
	"net"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// Formula is a valid file in which free-text cells that land in event
	// attributes start with each of = + - @ tab and CR (one cell per trigger;
	// the harness requires every one to arrive byte for byte). It is optional
	// only because the harness also derives such cells itself: it takes the
	// attribute values of Good, finds each in the bytes, prefixes it with each
	// trigger and parses again. Nil Formula therefore does not skip the check;
	// it fails unless Good yields attribute values that are whole cells of the
	// file, or NoFreeText is set.
	Formula []byte
	// NoFreeText declares that no event attribute carries free text from the
	// file (every attribute is a number, a fixed word or a derived label), so
	// rejecting a typed cell that starts with = + - @ is correct. It skips the
	// harness-generated cells entirely; a non-nil Formula fixture is still
	// checked.
	NoFreeText bool
	// KeepsCellValues declares that the importer keeps whole cell values in
	// event attributes by design (counter paths, log or provider names, pool
	// names, a free-text name). FuzzParse then does not fail on the sentinel
	// appearing in an attribute key or value, since a sentinel in such a cell
	// parses into a legitimate attribute. It still fails on the sentinel in an
	// error, a known gap, a field rule, an event field other than Attrs
	// (host label, kind, unit) or any other report text. Run's sentinel check
	// is unaffected: its Bad fixture must put the sentinel in a cell that is
	// not kept. The default (false) keeps the strict check.
	KeepsCellValues bool
	// NeverDetected declares an internal-format importer whose Detect always
	// returns 0 (no upload is ever picked for it). The harness then requires 0
	// for every head instead of requiring a positive score for Good.
	NeverDetected bool
	// Large is an optional valid file of more than 2*importer.HeadSize bytes.
	// The byte-limit and read-error checks need a file that is bigger than the
	// head Prepare peeks, so the breach or failure happens inside Parse. When
	// nil the harness builds one: the first line of Good, then the remaining
	// lines of Good repeated. Supply Large when that does not parse cleanly
	// (a format with a multi-line preamble, a closing line, a checksum).
	Large []byte
	// Options is the base Options (Host, Hosts, Supplied and so on). CaseID,
	// ImportNonce, FileName and Pseudonymize are filled in when empty. Give
	// Hosts (or Host) a mapping for every host name in Good: Event.Host must
	// carry the mapped label, never the raw name from the file.
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
	t.Run("event-ids", func(t *testing.T) { eventIDs(t, imp, fx) })
	t.Run("hosts-are-labels", func(t *testing.T) { hostLabels(t, imp, fx) })
	t.Run("detect", func(t *testing.T) { detect(t, imp, fx) })
	t.Run("archives-refused", func(t *testing.T) { archives(t, imp, fx) })
	t.Run("limit-bytes", func(t *testing.T) { limitBytes(t, imp, fx) })
	t.Run("limit-rows", func(t *testing.T) { limitRows(t, imp, fx) })
	t.Run("limit-line", func(t *testing.T) { limitLine(t, imp, fx) })
	t.Run("read-errors-surface", func(t *testing.T) { readErrors(t, imp, fx) })
	t.Run("emit-errors-surface", func(t *testing.T) { emitErrors(t, imp, fx) })
	t.Run("encodings-identical", func(t *testing.T) { encodings(t, imp, fx) })
	t.Run("line-endings-identical", func(t *testing.T) { lineEndings(t, imp, fx) })
	t.Run("sentinel-never-leaks", func(t *testing.T) { sentinel(t, imp, fx) })
	t.Run("formula-cells-verbatim", func(t *testing.T) { formula(t, imp, fx) })
	t.Run("truncated-and-tiny-files", func(t *testing.T) { tiny(t, imp, fx) })
	t.Run("context-cancel", func(t *testing.T) { cancellation(t, imp, fx) })
	t.Run("context-cancel-in-parse", func(t *testing.T) { cancellationInParse(t, imp, fx) })
	t.Run("concurrent-parse", func(t *testing.T) { concurrent(t, imp, fx) })
	t.Run("no-network", func(t *testing.T) { noNetwork(t, imp, fx) })
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
		if fx.NeverDetected {
			if s != 0 {
				t.Errorf("Detect(%s) = %d, want 0 for an importer that is never picked by content", name, s)
			}
		} else if name == "good" && s == 0 {
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

// largeFixture returns a valid file bigger than 2*HeadSize and the number of
// events it yields. A byte limit smaller than HeadSize is always caught by
// Prepare's head peek, before Parse runs; only a larger file puts the breach
// (or a read error) inside Parse, which is where an importer can mishandle it.
func largeFixture(t *testing.T, imp importer.Importer, fx Fixtures) ([]byte, int) {
	t.Helper()
	min := 2*importer.HeadSize + 1
	large := fx.Large
	if large == nil {
		lines := bytes.SplitAfter(fx.Good, []byte("\n"))
		if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
			lines = lines[:n-1]
		}
		if len(lines) < 2 {
			t.Fatal("Good fixture needs a header line and at least one more to derive a large file; set Fixtures.Large")
		}
		for i, l := range lines {
			if !bytes.HasSuffix(l, []byte("\n")) {
				lines[i] = append(append([]byte(nil), l...), '\n')
			}
		}
		var b bytes.Buffer
		b.Write(lines[0])
		for b.Len() < min {
			for _, l := range lines[1:] {
				b.Write(l)
			}
		}
		large = b.Bytes()
	}
	if len(large) < min {
		t.Fatalf("Fixtures.Large is %d bytes, want more than %d (twice importer.HeadSize)", len(large), 2*importer.HeadSize)
	}
	o := fx.opts()
	o.Limits.MaxBytes = 1 << 40
	o.Limits.MaxRows = 1 << 40
	evs, _, err := collect(imp, large, o)
	if err != nil || len(evs) == 0 {
		t.Fatalf("the large file (Good repeated, or Fixtures.Large) does not parse: %d events, %v; set Fixtures.Large to a valid file over %d bytes", len(evs), err, 2*importer.HeadSize)
	}
	return large, len(evs)
}

func limitBytes(t *testing.T, imp importer.Importer, fx Fixtures) {
	// A small file: the breach is found while the head is peeked.
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

	// A large file: the breach happens inside Parse, after the head. Parse
	// itself must return the LimitError; an importer that treats any read error
	// as the end of the file would finish "ok" with a truncated import.
	large, n := largeFixture(t, imp, fx)
	o = fx.opts()
	o.Limits.MaxBytes = importer.HeadSize + int64(len(large)-importer.HeadSize)/2
	o.Limits.MaxRows = 1 << 40
	var emitted int
	var err2 error
	safely(t, "parse of a large file over MaxBytes", func() { emitted, _, err2 = parseCounting(imp, large, o) })
	wantLimit(t, fmt.Sprintf("Parse of %d bytes with MaxBytes %d (the limit is hit after the head, inside Parse)", len(large), o.Limits.MaxBytes), "bytes", err2)
	if emitted >= n {
		t.Errorf("parse emitted %d of %d events past a byte-limit breach", emitted, n)
	}
	evs, _, err = collect(imp, large, o)
	wantLimit(t, "Collect of a large file over MaxBytes", "bytes", err)
	if len(evs) != 0 {
		t.Errorf("%d events returned after a byte-limit breach", len(evs))
	}
	o.Limits.MaxBytes = int64(len(large)) - 1
	if _, _, err := collect(imp, large, o); err == nil {
		t.Errorf("a large file one byte over MaxBytes was accepted")
	}
	o.Limits.MaxBytes = int64(len(large))
	if _, _, err := collect(imp, large, o); err != nil {
		t.Errorf("a large file of exactly MaxBytes must pass: %v", err)
	}
}

// failingReader yields data[:cut] and then fails with err, either on its own
// call or together with the last bytes.
type failingReader struct {
	data     []byte
	off, cut int
	err      error
	together bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.off >= f.cut {
		return 0, f.err
	}
	end := f.off + len(p)
	if end > f.cut {
		end = f.cut
	}
	n := copy(p, f.data[f.off:end])
	f.off += n
	if f.together && f.off >= f.cut {
		return n, f.err
	}
	return n, nil
}

var errRead = errors.New("harness: simulated read failure")

// readErrors checks that a failing reader fails the import. A file cut short
// by an I/O error must never come back as a smaller successful import.
func readErrors(t *testing.T, imp importer.Importer, fx Fixtures) {
	large, _ := largeFixture(t, imp, fx)
	cuts := []int{20, importer.HeadSize + 100, importer.HeadSize + 101, len(large) / 2}
	for _, cut := range cuts {
		for _, together := range []bool{false, true} {
			name := fmt.Sprintf("error after %d bytes (with the last data: %v)", cut, together)
			var evs []evidence.Event
			var err error
			ok := safely(t, name, func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				o := fx.opts()
				o.Limits.MaxBytes = 1 << 40
				o.Limits.MaxRows = 1 << 40
				evs, _, err = importer.Collect(ctx, imp, &failingReader{data: large, cut: cut, err: errRead, together: together}, o)
			})
			if !ok {
				continue
			}
			switch {
			case err == nil:
				t.Errorf("%s: the import succeeded with %d events; a read error must fail it, not end the file", name, len(evs))
			case !errors.Is(err, errRead):
				t.Errorf("%s: got %v, want an error wrapping the read error", name, err)
			}
			if len(evs) != 0 {
				t.Errorf("%s: %d events returned with an error", name, len(evs))
			}
		}
	}
}

var errEmit = errors.New("harness: emit refused the event")

// emitErrors calls Parse directly with an emit that refuses the k-th event.
// Parse must stop and return that error; it must not carry on, nor return nil.
func emitErrors(t *testing.T, imp importer.Importer, fx Fixtures) {
	baseline, _, err := parseCounting(imp, fx.Good, fx.opts())
	if err != nil || baseline == 0 {
		t.Fatalf("baseline parse: %d events, %v", baseline, err)
	}
	for k := 1; k <= 3 && k <= baseline; k++ {
		var calls int
		var perr error
		safely(t, fmt.Sprintf("emit refusing event %d", k), func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			body, o, err := importer.Prepare(imp, bytes.NewReader(fx.Good), fx.opts())
			if err != nil {
				t.Errorf("Prepare: %v", err)
				return
			}
			_, perr = imp.Parse(ctx, body, o, func(evidence.Event) error {
				calls++
				if calls >= k {
					return errEmit
				}
				return nil
			})
		})
		switch {
		case perr == nil:
			t.Errorf("emit refused event %d and Parse returned nil: a refused event must fail the import", k)
		case !errors.Is(perr, errEmit):
			t.Errorf("emit refused event %d: Parse returned %v, want an error wrapping the refusal", k, perr)
		}
		if calls != k {
			t.Errorf("emit refused event %d but was called %d times; Parse must stop at the first refusal", k, calls)
		}
	}
}

// eventIDs checks the provenance fields every event carries.
func eventIDs(t *testing.T, imp importer.Importer, fx Fixtures) {
	o := fx.opts()
	evs, rep, err := collect(imp, fx.Good, o)
	if err != nil || len(evs) == 0 {
		t.Fatalf("Good fixture: %d events, %v", len(evs), err)
	}
	if int64(len(evs)) != rep.Emitted {
		t.Errorf("report says %d events emitted, %d were received", rep.Emitted, len(evs))
	}
	ids := map[string]bool{}
	for i, ev := range evs {
		if err := evidence.CheckEventID(ev.EvidenceID, o.CaseID, imp.Name()); err != nil {
			t.Errorf("event %d: %v (build the ID with evidence.EventID(opts.CaseID, Name(), opts.ImportNonce, line, ordinal))", i, err)
		}
		if ev.Line < 1 {
			t.Errorf("event %d: Line %d, want the 1-based source line", i, ev.Line)
		}
		if ev.TZRule == "" {
			t.Errorf("event %d: TZRule is empty", i)
		}
		if ev.Source != imp.Name() {
			t.Errorf("event %d: source %q, want %q", i, ev.Source, imp.Name())
		}
		ids[ev.EvidenceID] = true
	}
	// The ID depends on the nonce and the case: a second import of the same
	// bytes, or the same bytes in another case, never collides.
	o2 := o
	o2.ImportNonce = "harness-nonce-2"
	evs2, _, err := collect(imp, fx.Good, o2)
	if err != nil {
		t.Fatalf("Good fixture with another nonce: %v", err)
	}
	for _, ev := range evs2 {
		if ids[ev.EvidenceID] {
			t.Errorf("an evidence id repeats across two imports: the ImportNonce is not part of it")
			break
		}
	}
	o3 := o
	o3.CaseID = "qqqqqqqqqqqqqqqqqqqqqqqqqq"
	evs3, _, err := collect(imp, fx.Good, o3)
	if err != nil {
		t.Fatalf("Good fixture in another case: %v", err)
	}
	for _, ev := range evs3 {
		if ids[ev.EvidenceID] {
			t.Errorf("an evidence id repeats across two cases: the CaseID is not part of it")
			break
		}
	}
}

// hostLabels checks that Event.Host is a label the owner picked or mapped,
// never a raw name read from the file.
func hostLabels(t *testing.T, imp importer.Importer, fx Fixtures) {
	o := fx.opts()
	evs, rep, err := collect(imp, fx.Good, o)
	if err != nil || len(evs) == 0 {
		t.Fatalf("Good fixture: %d events, %v", len(evs), err)
	}
	allowed := map[string]bool{"": true}
	if o.Host != nil {
		allowed[o.Host.Label] = true
	}
	if o.Hosts != nil {
		for _, raw := range rep.ObservedHosts {
			if h, ok := o.Hosts(raw); ok {
				allowed[h.Label] = true
			}
		}
	}
	for i, ev := range evs {
		if !allowed[ev.Host] {
			t.Errorf("event %d: Host is not a mapped label: raw host names must go through Options.Host or Options.Hosts and be reported with Report.Observe", i)
			break
		}
	}

	// Nothing maps: the raw names must not surface anywhere. The importer may
	// refuse the file (unknown-host) or emit events without a host.
	raws := rep.ObservedHosts
	if len(raws) == 0 {
		return
	}
	o2 := fx.opts()
	o2.Host = nil
	o2.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	var evs2 []evidence.Event
	var rep2 importer.Report
	var err2 error
	safely(t, "Good fixture, no host mapping", func() { evs2, rep2, err2 = collect(imp, fx.Good, o2) })
	// Everything a run exposes except Report.ObservedHosts, the one place raw
	// names belong.
	var out []string
	for e := err2; e != nil; e = errors.Unwrap(e) {
		out = append(out, e.Error(), fmt.Sprintf("%#v", e))
	}
	out = append(out, rep2.KnownGaps...)
	for _, v := range rep2.FieldRules {
		out = append(out, v)
	}
	for _, re := range rep2.Errors {
		out = append(out, re.Error())
	}
	for _, ev := range evs2 {
		out = append(out, fmt.Sprintf("%#v", ev))
	}
	for _, s := range out {
		for _, raw := range raws {
			if len(raw) >= 3 && strings.Contains(s, raw) {
				t.Errorf("with no host mapping the raw host name %q appears in an error, event or report", raw)
				return
			}
		}
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
// the events. Report.ObservedHosts is left out: it holds raw host names by
// contract (the service maps or refuses them), and the rule that labels, not
// raw names, reach events is checked by hosts-are-labels. With keepCells the
// event attribute values and keys are left out too, for an importer that
// declares Fixtures.KeepsCellValues; everything else is still checked.
func exposed(rep importer.Report, evs []evidence.Event, err error, keepCells bool) []string {
	var out []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		out = append(out, e.Error(), fmt.Sprintf("%#v", e))
	}
	// The report is printed field by field: printing it whole would include
	// ObservedHosts and the unexported index over it.
	view := struct {
		Importer                string
		Rows, Emitted, Rejected int64
		Errors                  []importer.RowError
		First, Last             time.Time
		Quality                 evidence.SourceQuality
		FieldRules              map[string]string
		KnownGaps               []string
	}{rep.Importer, rep.Rows, rep.Emitted, rep.Rejected, rep.Errors, rep.First, rep.Last, rep.Quality, rep.FieldRules, rep.KnownGaps}
	out = append(out, fmt.Sprintf("%#v", view), fmt.Sprintf("%+v", view))
	out = append(out, rep.KnownGaps...)
	for _, v := range rep.FieldRules {
		out = append(out, v)
	}
	for _, ev := range evs {
		attrs := ev.Attrs
		if keepCells {
			ev.Attrs = nil
		}
		out = append(out, fmt.Sprintf("%#v", ev))
		if !keepCells {
			for k, v := range attrs {
				out = append(out, k, v)
			}
		}
	}
	return out
}

func leaks(t testing.TB, what string, rep importer.Report, evs []evidence.Event, err error) {
	t.Helper()
	leaksWith(t, what, rep, evs, err, false)
}

func leaksWith(t testing.TB, what string, rep importer.Report, evs []evidence.Event, err error, keepCells bool) {
	t.Helper()
	for _, s := range exposed(rep, evs, err, keepCells) {
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

// attrValues lists the distinct attribute values of evs, sorted.
func attrValues(evs []evidence.Event) []string {
	seen := map[string]bool{}
	for _, ev := range evs {
		for _, v := range ev.Attrs {
			seen[v] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// escaped reports an attribute that was given a formula-escaping apostrophe.
func escaped(evs []evidence.Event) (string, bool) {
	for _, ev := range evs {
		for k, v := range ev.Attrs {
			if strings.HasPrefix(v, "'") && startsWithTrigger(v[1:]) {
				return k, true
			}
		}
	}
	return "", false
}

// formula checks that cells starting with = + - @ tab or CR reach the event
// attributes untouched; escaping them is the exporter's job. The check cannot
// be skipped by leaving Fixtures.Formula out: the harness then builds the
// cells itself by prefixing the free-text values it finds in Good.
func formula(t *testing.T, imp importer.Importer, fx Fixtures) {
	o := fx.opts()
	o.Limits.MaxRejectFraction = 1

	if len(fx.Formula) > 0 {
		evs, _, err := collect(imp, fx.Formula, o)
		if err != nil {
			t.Fatalf("Formula fixture refused: %v", err)
		}
		for _, tr := range triggers {
			found := false
			for _, v := range attrValues(evs) {
				if strings.HasPrefix(v, tr) {
					found = true
				}
			}
			if !found {
				t.Errorf("no attribute value starts with %q: the cell was dropped, trimmed or altered, or Fixtures.Formula has no such cell", tr)
			}
		}
		if k, bad := escaped(evs); bad {
			t.Errorf("attribute %q was escaped; escaping belongs to export, not import", k)
		}
	}

	// Generated cells: prefix each attribute value of Good that is a whole cell
	// of the file with each trigger. An importer whose attributes are all typed
	// (numbers, fixed words, derived labels) declares NoFreeText: it has no
	// free-text cell to carry a formula, and rejecting a typed cell that starts
	// with a trigger is correct, so the generated check does not apply to it.
	if fx.NoFreeText {
		return
	}
	base, _, err := collect(imp, fx.Good, o)
	if err != nil {
		t.Fatalf("Good fixture refused: %v", err)
	}
	type cand struct {
		v    string
		idx  int
		text bool
	}
	var cands []cand
	for _, v := range attrValues(base) {
		if v == "" || startsWithTrigger(v) {
			continue
		}
		if i := cellIndex(fx.Good, v); i >= 0 {
			cands = append(cands, cand{v, i, strings.ContainsAny(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")})
		}
	}
	// Free-text (letter-containing) values first: they are the ones a parser
	// most plausibly keeps as text, and digit-led values must not crowd them out.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].text && !cands[j].text })
	if len(cands) > 40 {
		cands = cands[:40]
	}
	if len(cands) == 0 {
		t.Fatal("no attribute value of the Good fixture appears verbatim as a whole cell of its bytes, so the formula check has nothing to prefix: " +
			"supply Fixtures.Formula, make Good carry a free-text cell into an attribute, or set Fixtures.NoFreeText if no attribute holds file text")
	}
	for _, tr := range triggers {
		parsed, verbatim := 0, 0
		var rejected int64
		for _, c := range cands {
			data := append(append(append([]byte(nil), fx.Good[:c.idx]...), tr...), fx.Good[c.idx:]...)
			var evs []evidence.Event
			var rep importer.Report
			var err error
			if !safely(t, fmt.Sprintf("Good with %q before %q", tr, c.v), func() { evs, rep, err = collect(imp, data, o) }) {
				continue
			}
			if err != nil {
				continue // a refused file is a legitimate answer to a changed cell
			}
			parsed++
			rejected += rep.Rejected
			for _, v := range attrValues(evs) {
				if v == tr+c.v {
					verbatim++
				}
			}
			if k, bad := escaped(evs); bad {
				t.Errorf("attribute %q was escaped; escaping belongs to export, not import", k)
			}
		}
		// A prefixed cell may be refused, quarantined or (tab, CR) read as a
		// field or line separator: those are answers to a changed cell. What is
		// not acceptable is a parse that accepted every row yet kept none of the
		// prefixed values: the leading character was dropped or altered.
		if parsed > 0 && verbatim == 0 && rejected == 0 {
			t.Errorf("%d files with a cell starting with %q parsed without rejecting a row, but no attribute arrived as %q+value: the leading character was dropped, trimmed or altered", parsed, tr, tr)
		}
	}
}

// cellIndex returns the offset in data of the first occurrence of v that is a
// whole cell: it is bounded on both sides by a separator, a quote, a line end
// or the edge of the file. Matching whole cells keeps a value such as "2001"
// from being found inside a timestamp.
func cellIndex(data []byte, v string) int {
	sep := func(b byte) bool {
		switch b {
		case ',', '\t', ';', '|', '\r', '\n', '"':
			return true
		}
		return false
	}
	for off := 0; off < len(data); {
		i := bytes.Index(data[off:], []byte(v))
		if i < 0 {
			return -1
		}
		i += off
		end := i + len(v)
		if (i == 0 || sep(data[i-1])) && (end == len(data) || sep(data[end])) {
			return i
		}
		off = i + 1
	}
	return -1
}

// lineEndings checks that LF and CRLF copies of the Good file are the same
// import.
func lineEndings(t *testing.T, imp importer.Importer, fx Fixtures) {
	lf := bytes.ReplaceAll(fx.Good, []byte("\r\n"), []byte("\n"))
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	a, repA, errA := collect(imp, lf, fx.opts())
	b, repB, errB := collect(imp, crlf, fx.opts())
	if errA != nil || errB != nil {
		t.Fatalf("LF copy: %v; CRLF copy: %v", errA, errB)
	}
	if len(a) != len(b) {
		t.Fatalf("LF copy yields %d events, CRLF copy %d", len(a), len(b))
	}
	norm := func(m map[string]string) map[string]string {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = strings.ReplaceAll(v, "\r\n", "\n")
		}
		return out
	}
	for i := range a {
		x, y := a[i], b[i]
		xa, ya := norm(x.Attrs), norm(y.Attrs)
		x.Attrs, y.Attrs = nil, nil
		if !reflect.DeepEqual(x, y) || !reflect.DeepEqual(xa, ya) {
			t.Errorf("event %d differs between the LF and the CRLF copy", i)
		}
	}
	if repA.Rows != repB.Rows || repA.Emitted != repB.Emitted || repA.Rejected != repB.Rejected {
		t.Errorf("LF and CRLF copies report different counts")
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

// cancellationInParse calls Parse directly with an emit that ignores the
// context, on endless input of valid rows and of bad rows. Parse must notice
// the cancellation itself, not rely on emit failing: a stream of rejected rows
// never reaches emit at all.
func cancellationInParse(t *testing.T, imp importer.Importer, fx Fixtures) {
	for name, src := range map[string][]byte{"good rows": fx.Good, "bad rows": fx.Bad} {
		chunk := append([]byte(nil), src...)
		if len(chunk) == 0 || chunk[len(chunk)-1] != '\n' {
			chunk = append(chunk, '\n')
		}
		o := fx.opts()
		o.Limits.MaxBytes = 1 << 40
		o.Limits.MaxRows = 1 << 40
		o.Limits.MaxRejectFraction = 1

		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- fmt.Errorf("panic: %v", r)
				}
			}()
			body, o, err := importer.Prepare(imp, &endless{chunk: chunk, deadline: time.Now().Add(10 * time.Second)}, o)
			if err != nil {
				done <- err
				return
			}
			_, err = imp.Parse(ctx, body, o, func(evidence.Event) error { return nil })
			done <- err
		}()
		time.Sleep(50 * time.Millisecond)
		cancelled := time.Now()
		stop()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: Parse of an endless input returned without error", name)
			} else if strings.HasPrefix(err.Error(), "panic:") {
				t.Errorf("%s: %v", name, err)
			} else if d := time.Since(cancelled); d > time.Second {
				t.Errorf("%s: Parse took %v to stop after cancel, want under 1s", name, d)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: Parse did not stop within 1s of context cancellation (emit ignores ctx, so Parse must check it)", name)
		}
	}
}

// concurrent runs the importer from several goroutines at once. An importer is
// a value used by many imports; it must keep no state between calls.
func concurrent(t *testing.T, imp importer.Importer, fx Fixtures) {
	want, wantRep, err := collect(imp, fx.Good, fx.opts())
	if err != nil {
		t.Fatalf("Good fixture refused: %v", err)
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					bad.Add(1)
				}
			}()
			for i := 0; i < 5; i++ {
				got, rep, err := collect(imp, fx.Good, fx.opts())
				if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(rep, wantRep) {
					bad.Add(1)
				}
				collect(imp, fx.Bad, fx.opts())
			}
		}()
	}
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Errorf("%d concurrent parses of the same bytes differed from a single parse or panicked", n)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// noNetwork replaces the default HTTP transport and the default resolver with
// ones that count and fail, then runs the importer. Importers read bytes and
// nothing else (plan §0).
func noNetwork(t *testing.T, imp importer.Importer, fx Fixtures) {
	var calls atomic.Int64
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("importertest: network use from an importer")
	})
	oldT, oldC := http.DefaultTransport, http.DefaultClient.Transport
	oldPrefer, oldDial := net.DefaultResolver.PreferGo, net.DefaultResolver.Dial
	http.DefaultTransport, http.DefaultClient.Transport = rt, rt
	net.DefaultResolver.PreferGo = true
	net.DefaultResolver.Dial = func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("importertest: network use from an importer")
	}
	defer func() {
		http.DefaultTransport, http.DefaultClient.Transport = oldT, oldC
		net.DefaultResolver.PreferGo, net.DefaultResolver.Dial = oldPrefer, oldDial
	}()
	safely(t, "Good fixture", func() { collect(imp, fx.Good, fx.opts()) })
	safely(t, "Bad fixture", func() { collect(imp, fx.Bad, fx.opts()) })
	safely(t, "Detect", func() { imp.Detect(fx.Good, "fixture.txt") })
	if n := calls.Load(); n != 0 {
		t.Errorf("the importer made %d network calls; importers make none", n)
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
			if verr := ev.ValidateFor(o.CaseID, imp.Name(), imp.AllowedAttrs()); verr != nil {
				t.Fatalf("event %d: %v", i, verr)
			}
		}
		checkReport(t, rep)
		if bytes.Contains(data, []byte(Sentinel)) {
			leaksWith(t, "fuzz input", rep, evs, err, fx.KeepsCellValues)
		}
	})
}
