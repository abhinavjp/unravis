package importer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// fake is a tiny importer: one event per non-empty line, a line starting with
// "bad" is rejected, "boom" returns an error, "panic" panics in Detect.
type fake struct {
	name  string
	score int
	lim   importer.Limits
	emits func(ev evidence.Event) error
}

func (f *fake) Name() string                     { return f.name }
func (f *fake) Detect(head []byte, _ string) int { return f.score }
func (f *fake) AllowedAttrs() map[string]bool    { return map[string]bool{"k": true} }
func (f *fake) DefaultLimits() importer.Limits   { return f.lim }
func (f *fake) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rep := importer.Report{Importer: f.name}
	data, err := io.ReadAll(r)
	if err != nil {
		return rep, err
	}
	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		rep.Rows++
		switch {
		case strings.HasPrefix(line, "bad"):
			rep.Reject(int64(i+1), importer.ReasonBadTime)
			continue
		case strings.HasPrefix(line, "boom"):
			return rep, errors.New("boom")
		case strings.HasPrefix(line, "invalid"):
			ev := evidence.Event{EvidenceID: "x", Source: f.name, Kind: "k", Start: base.In(time.FixedZone("x", 3600)), End: base}
			if err := emit(ev); err != nil {
				return rep, err
			}
			continue
		case strings.HasPrefix(line, "attr"):
			ev := evidence.Event{EvidenceID: "x", Source: f.name, Kind: "k", Start: base, End: base, Attrs: map[string]string{"nope": "v"}}
			if err := emit(ev); err != nil {
				return rep, err
			}
			continue
		}
		ev := evidence.Event{
			EvidenceID: evidence.EventID(opts.CaseID, f.name, opts.ImportNonce, int64(i+1), 0),
			Source:     f.name, Kind: "k", Start: base, End: base, Line: int64(i + 1),
			Attrs: map[string]string{"k": line},
		}
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
	}
	return rep, nil
}

func TestSniffRefusesArchives(t *testing.T) {
	heads := map[string][]byte{
		"zip":     []byte("PK\x03\x04rest"),
		"zip-end": []byte("PK\x05\x06"),
		"gzip":    {0x1f, 0x8b, 8, 0},
		"7z":      {'7', 'z', 0xbc, 0xaf, 0x27, 0x1c, 0},
		"rar":     []byte("Rar!\x1a\x07\x00"),
		"xz":      {0xfd, '7', 'z', 'X', 'Z', 0},
		"bzip2":   []byte("BZh9\x31AY"),
		"cab":     []byte("MSCF\x00\x00\x00\x00"),
	}
	for name, h := range heads {
		err := importer.Sniff(h)
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonArchive || !errors.Is(err, importer.ErrQuarantine) {
			t.Errorf("%s: %v", name, err)
		}
		if !strings.Contains(err.Error(), "extract first") {
			t.Errorf("%s: message %q", name, err)
		}
	}
	for name, h := range map[string][]byte{
		"empty": nil, "text": []byte("date time\n"), "bz-not-digit": []byte("BZhx"), "bz-short": []byte("BZh"),
		"pk-text": []byte("PKxx"), "utf16": {0xff, 0xfe, 'a', 0},
	} {
		if err := importer.Sniff(h); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Only the first 64 KiB are looked at.
	if err := importer.Sniff(append(bytes.Repeat([]byte("a"), importer.HeadSize), "PK\x03\x04"...)); err != nil {
		t.Errorf("magic past the head: %v", err)
	}
}

// oneByte yields one byte per Read, to exercise the probe logic.
type oneByte struct{ r io.Reader }

func (o oneByte) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestLimitReader(t *testing.T) {
	for _, slow := range []bool{false, true} {
		wrap := func(s string) io.Reader {
			var r io.Reader = strings.NewReader(s)
			if slow {
				r = oneByte{r}
			}
			return r
		}
		got, err := io.ReadAll(importer.LimitReader(wrap("12345"), 5))
		if err != nil || string(got) != "12345" {
			t.Errorf("exactly max: %q %v", got, err)
		}
		got, err = io.ReadAll(importer.LimitReader(wrap("123456"), 5))
		var le *importer.LimitError
		if !errors.As(err, &le) || le.Limit != "bytes" || le.Max != 5 || le.At != 6 {
			t.Errorf("over: %q %v", got, err)
		}
		if len(got) > 5 {
			t.Errorf("returned %d bytes past the limit", len(got))
		}
		// Sticky.
		lr := importer.LimitReader(wrap("123456"), 5)
		_, _ = io.ReadAll(lr)
		if _, err := lr.Read(make([]byte, 4)); !errors.As(err, &le) {
			t.Errorf("not sticky: %v", err)
		}
		// No limit.
		got, err = io.ReadAll(importer.LimitReader(wrap("abc"), 0))
		if err != nil || string(got) != "abc" {
			t.Errorf("max 0: %q %v", got, err)
		}
		// Empty source with a limit is fine.
		if got, err := io.ReadAll(importer.LimitReader(wrap(""), 3)); err != nil || len(got) != 0 {
			t.Errorf("empty: %q %v", got, err)
		}
	}
}

func TestLimits(t *testing.T) {
	l := importer.NewLimits(100, 10)
	if l.MaxBytes != 100 || l.MaxRows != 10 || l.MaxLineBytes != importer.DefaultMaxLineBytes ||
		l.MaxFields != importer.DefaultMaxFields || l.MaxRejectFraction != importer.DefaultMaxRejectFraction {
		t.Fatalf("%+v", l)
	}
	if (importer.Limits{MaxLineBytes: 7}).WithDefaults().MaxLineBytes != 7 {
		t.Error("WithDefaults overwrote a set value")
	}
	if err := l.CheckRows(10); err != nil {
		t.Errorf("at max: %v", err)
	}
	var le *importer.LimitError
	if err := l.CheckRows(11); !errors.As(err, &le) || le.Limit != "rows" || le.At != 11 || !errors.Is(err, importer.ErrLimit) {
		t.Errorf("over: %v", err)
	}
	if err := (importer.Limits{}).CheckRows(1 << 40); err != nil {
		t.Errorf("no row limit: %v", err)
	}
	var qe *importer.QuarantineError
	if err := l.CheckRejects(100, 5); err != nil {
		t.Errorf("5%% is allowed: %v", err)
	}
	if err := l.CheckRejects(100, 6); !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction {
		t.Errorf("6%%: %v", err)
	}
	if err := l.CheckRejects(0, 0); err != nil {
		t.Errorf("no rows: %v", err)
	}
	if err := (importer.Limits{}).CheckRejects(10, 1); !errors.As(err, &qe) {
		t.Errorf("zero value uses the default fraction: %v", err)
	}
}

func TestErrorsAndReasons(t *testing.T) {
	var err error = fmt.Errorf("wrapped: %w", &importer.LimitError{Limit: "rows", Max: 1, At: 2})
	if !errors.Is(err, importer.ErrLimit) || errors.Is(err, importer.ErrQuarantine) {
		t.Error("LimitError identity")
	}
	err = fmt.Errorf("wrapped: %w", &importer.QuarantineError{Reason: importer.ReasonTZMismatch, Column: "time"})
	var qe *importer.QuarantineError
	if !errors.Is(err, importer.ErrQuarantine) || !errors.As(err, &qe) || qe.Column != "time" {
		t.Error("QuarantineError identity")
	}
	if importer.ErrUnknownHost.Error() != "unknown host" {
		t.Errorf("unknown host wording %q", importer.ErrUnknownHost)
	}
	seen := map[string]bool{}
	for _, r := range importer.AllReasons() {
		if !r.Valid() || r.Text() == "" || r.Text() == "refused" {
			t.Errorf("reason %q: valid=%v text=%q", r, r.Valid(), r.Text())
		}
		if seen[string(r)] {
			t.Errorf("duplicate reason %q", r)
		}
		seen[string(r)] = true
	}
	if importer.Reason("made-up").Valid() || importer.Reason("made-up").Text() != "refused" {
		t.Error("unknown reason")
	}
	if importer.ReasonArchive.Text() != "archive, extract first" {
		t.Errorf("archive text %q", importer.ReasonArchive.Text())
	}
	if got := (importer.RowError{Line: 7, Reason: importer.ReasonEncoding}).Error(); got != "line 7: encoding" {
		t.Errorf("RowError %q", got)
	}
	// The slice returned by AllReasons is a copy.
	rs := importer.AllReasons()
	rs[0] = "x"
	if importer.AllReasons()[0] == "x" {
		t.Error("AllReasons aliases internal state")
	}
}

func TestReportHelpers(t *testing.T) {
	var r importer.Report
	for i := 0; i < importer.MaxReportErrors+20; i++ {
		r.Reject(int64(i), importer.ReasonBadNumber)
	}
	if r.Rejected != int64(importer.MaxReportErrors+20) || len(r.Errors) != importer.MaxReportErrors {
		t.Errorf("rejected=%d kept=%d", r.Rejected, len(r.Errors))
	}
	for i := 0; i < importer.MaxObservedHosts; i++ {
		if err := r.Observe(fmt.Sprintf("h%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Observe("h0"); err != nil {
		t.Errorf("repeat host: %v", err)
	}
	var le *importer.LimitError
	if err := r.Observe("one-more"); !errors.As(err, &le) || le.Limit != "hosts" {
		t.Errorf("too many hosts: %v", err)
	}
	if len(r.ObservedHosts) != importer.MaxObservedHosts {
		t.Errorf("hosts kept %d", len(r.ObservedHosts))
	}
	a := time.Date(2001, 1, 1, 12, 0, 0, 0, time.FixedZone("p", 3600))
	b := time.Date(2001, 1, 1, 9, 0, 0, 0, time.UTC)
	r.Cover(a)
	r.Cover(b)
	if !r.First.Equal(b) || !r.Last.Equal(a) || r.First.Location() != time.UTC || r.Last.Location() != time.UTC {
		t.Errorf("cover %v %v", r.First, r.Last)
	}
	r.Rule("time", "utc-field")
	r.Rule("time", "header-bias")
	if len(r.FieldRules) != 1 || r.FieldRules["time"] != "header-bias" {
		t.Errorf("rules %v", r.FieldRules)
	}
	r.Gap("one")
	r.Gap("one")
	r.Gap("two")
	if len(r.KnownGaps) != 2 {
		t.Errorf("gaps %v", r.KnownGaps)
	}
}

func TestWindow(t *testing.T) {
	t0 := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	if !(importer.Window{}).Contains(t0) {
		t.Error("zero window")
	}
	w := importer.Window{From: t0, To: t1}
	for tm, want := range map[time.Time]bool{t0.Add(-1): false, t0: true, t1: true, t1.Add(1): false} {
		if w.Contains(tm) != want {
			t.Errorf("%v: want %v", tm, want)
		}
	}
	if !(importer.Window{From: t0}).Contains(t1.Add(24*time.Hour)) || (importer.Window{To: t1}).Contains(t1.Add(1)) {
		t.Error("open bounds")
	}
}

func TestRegistry(t *testing.T) {
	a := &fake{name: "a", score: 80}
	b := &fake{name: "b", score: 60}
	c := &fake{name: "c", score: 75}
	dup := &fake{name: "a", score: 100}
	reg := importer.NewRegistry(a, nil, b, c, dup)
	if got := strings.Join(reg.Names(), ","); got != "a,b,c" {
		t.Errorf("names %s", got)
	}
	if imp, ok := reg.ByName("a"); !ok || imp != importer.Importer(a) {
		t.Error("first registration must win")
	}
	if _, ok := reg.ByName("zzz"); ok {
		t.Error("unknown name found")
	}
	best, score, tied := reg.Detect(nil, "f")
	if best != importer.Importer(a) || score != 80 || strings.Join(tied, ",") != "a,c" {
		t.Errorf("best=%v score=%d tied=%v", best, score, tied)
	}
	// Clear winner: no tie.
	best, score, tied = importer.NewRegistry(a, b).Detect(nil, "f")
	if best != importer.Importer(a) || score != 80 || tied != nil {
		t.Errorf("clear winner: %v %d %v", best, score, tied)
	}
	// Exactly TieWindow apart is a tie, one more is not.
	_, _, tied = importer.NewRegistry(&fake{name: "x", score: 50}, &fake{name: "y", score: 50 + importer.TieWindow}).Detect(nil, "f")
	if strings.Join(tied, ",") != "x,y" {
		t.Errorf("at window: %v", tied)
	}
	_, _, tied = importer.NewRegistry(&fake{name: "x", score: 49}, &fake{name: "y", score: 50 + importer.TieWindow}).Detect(nil, "f")
	if tied != nil {
		t.Errorf("past window: %v", tied)
	}
	// Exact tie: earliest registered wins as best, both listed.
	best, _, tied = importer.NewRegistry(&fake{name: "m", score: 70}, &fake{name: "k", score: 70}).Detect(nil, "f")
	if best.Name() != "m" || strings.Join(tied, ",") != "k,m" {
		t.Errorf("exact tie: %v %v", best.Name(), tied)
	}
	// Nothing scores: no best.
	best, score, tied = importer.NewRegistry(&fake{name: "z", score: 0}).Detect(nil, "f")
	if best != nil || score != 0 || tied != nil {
		t.Errorf("none: %v %d %v", best, score, tied)
	}
	if best, _, _ := importer.NewRegistry().Detect(nil, "f"); best != nil {
		t.Error("empty registry")
	}
	// Scores are clamped.
	_, score, _ = importer.NewRegistry(&fake{name: "hi", score: 500}, &fake{name: "lo", score: -5}).Detect(nil, "f")
	if score != 100 {
		t.Errorf("clamp high: %d", score)
	}
	// A panicking importer scores 0 and does not stop the others.
	best, _, _ = importer.NewRegistry(&panicky{}, a).Detect(nil, "f")
	if best != importer.Importer(a) {
		t.Errorf("panic isolation: %v", best)
	}
	// Detect is given at most the head.
	big := &headLen{}
	importer.NewRegistry(big).Detect(make([]byte, importer.HeadSize+100), "f")
	if big.n != importer.HeadSize {
		t.Errorf("head passed = %d", big.n)
	}
}

type panicky struct{ fake }

func (*panicky) Name() string              { return "panicky" }
func (*panicky) Detect([]byte, string) int { panic("detect blew up") }

type headLen struct {
	fake
	n int
}

func (h *headLen) Name() string                     { return "headlen" }
func (h *headLen) Detect(head []byte, _ string) int { h.n = len(head); return 1 }

func collect(t *testing.T, f *fake, body string, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), f, strings.NewReader(body), opts)
}

func TestCollectHappyPath(t *testing.T) {
	f := &fake{name: "fake"}
	opts := importer.Options{CaseID: "abcdefghijkl", ImportNonce: "n1"}
	evs, rep, err := collect(t, f, "one\ntwo\n", opts)
	if err != nil || len(evs) != 2 || rep.Rows != 2 || rep.Emitted != 2 || rep.Importer != "fake" {
		t.Fatalf("evs=%d rep=%+v err=%v", len(evs), rep, err)
	}
	if !strings.HasPrefix(evs[0].EvidenceID, "ev:abcdefgh:fake:") {
		t.Errorf("id %q", evs[0].EvidenceID)
	}
	// Events are copies: changing one does not change another run's.
	evs[0].Attrs["k"] = "changed"
}

func TestCollectRefusals(t *testing.T) {
	var le *importer.LimitError
	var qe *importer.QuarantineError
	opts := importer.Options{CaseID: "c", ImportNonce: "n"}

	// Archive.
	evs, _, err := collect(t, &fake{name: "f"}, "PK\x03\x04junk", opts)
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonArchive || evs != nil {
		t.Errorf("archive: %v %v", err, evs)
	}
	// Byte limit from the importer's defaults, then overridden by Options.
	f := &fake{name: "f", lim: importer.Limits{MaxBytes: 5}}
	evs, _, err = collect(t, f, "one\ntwo\n", opts)
	if !errors.As(err, &le) || le.Limit != "bytes" || evs != nil {
		t.Errorf("byte limit: %v %v", err, evs)
	}
	opts2 := opts
	opts2.Limits.MaxBytes = 100
	if _, _, err = collect(t, f, "one\ntwo\n", opts2); err != nil {
		t.Errorf("override: %v", err)
	}
	// Exactly at the byte limit passes.
	opts3 := opts
	opts3.Limits.MaxBytes = 8
	if _, _, err = collect(t, &fake{name: "f"}, "one\ntwo\n", opts3); err != nil {
		t.Errorf("at limit: %v", err)
	}
	// Row limit applied after the parse.
	opts4 := opts
	opts4.Limits.MaxRows = 1
	evs, rep, err := collect(t, &fake{name: "f"}, "one\ntwo\n", opts4)
	if !errors.As(err, &le) || le.Limit != "rows" || evs != nil || rep.Rows != 2 {
		t.Errorf("rows: %v %v %+v", err, evs, rep)
	}
	// Reject fraction.
	evs, rep, err = collect(t, &fake{name: "f"}, "one\nbad\n", opts)
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction || evs != nil || rep.Rejected != 1 {
		t.Errorf("rejects: %v %v %+v", err, evs, rep)
	}
	// Tolerated rejects.
	opts5 := opts
	opts5.Limits.MaxRejectFraction = 0.6
	if evs, _, err = collect(t, &fake{name: "f"}, "one\nbad\n", opts5); err != nil || len(evs) != 1 {
		t.Errorf("tolerated: %v %v", err, evs)
	}
	// Importer error: no events, report still returned.
	evs, rep, err = collect(t, &fake{name: "f"}, "one\nboom\n", opts)
	if err == nil || evs != nil || rep.Importer != "f" {
		t.Errorf("importer error: %v %v %+v", err, evs, rep)
	}
	// Events that fail Validate are refused.
	for _, body := range []string{"invalid\n", "attr\n"} {
		evs, _, err = collect(t, &fake{name: "f"}, body, opts)
		if !errors.Is(err, evidence.ErrEventInvalid) || evs != nil {
			t.Errorf("%q: %v %v", body, err, evs)
		}
	}
	// A cancelled context stops before any work.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evs, _, err = importer.Collect(ctx, &fake{name: "f"}, strings.NewReader("one\n"), opts)
	if !errors.Is(err, context.Canceled) || evs != nil {
		t.Errorf("cancel: %v %v", err, evs)
	}
}

func TestPrepareKeepsHead(t *testing.T) {
	body := strings.Repeat("x", importer.HeadSize*2+17)
	r, opts, err := importer.Prepare(&fake{name: "f", lim: importer.Limits{MaxBytes: 1 << 20}}, strings.NewReader(body), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || string(got) != body {
		t.Fatalf("body altered: %d bytes, err %v", len(got), err)
	}
	if opts.Limits.MaxBytes != 1<<20 || opts.Limits.MaxFields != importer.DefaultMaxFields {
		t.Errorf("limits %+v", opts.Limits)
	}
	// Short and empty bodies.
	for _, s := range []string{"", "x"} {
		r, _, err := importer.Prepare(&fake{name: "f"}, strings.NewReader(s), importer.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := io.ReadAll(r); string(got) != s {
			t.Errorf("short body %q -> %q", s, got)
		}
	}
	// A body over the limit surfaces the limit even if it ends inside the head.
	_, _, err = importer.Prepare(&fake{name: "f", lim: importer.Limits{MaxBytes: 3}}, strings.NewReader("abcdef"), importer.Options{})
	var le *importer.LimitError
	if !errors.As(err, &le) {
		t.Errorf("limit in head: %v", err)
	}
}
