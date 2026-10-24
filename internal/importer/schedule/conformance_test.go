package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// both runs a test body against each importer with a fixture that suits it.
type subject struct {
	name    string
	imp     importer.Importer
	fixture string
	opts    func(testing.TB) importer.Options
}

func subjects() []subject {
	return []subject{
		{"hangfire", NewHangfire(), "hangfire_good.csv", func(testing.TB) importer.Options { return hangfireOpts() }},
		{"agent", NewAgent(), "agent_good.csv", func(t testing.TB) importer.Options { return agentOpts(t) }},
	}
}

func TestConformanceMetadata(t *testing.T) {
	if len(All()) != 2 {
		t.Fatalf("All() = %d importers", len(All()))
	}
	for _, s := range subjects() {
		if len(s.imp.AllowedAttrs()) > 16 {
			t.Errorf("%s: %d allowed attrs, max 16", s.name, len(s.imp.AllowedAttrs()))
		}
		l := s.imp.DefaultLimits()
		if l.MaxBytes != 16<<20 || l.MaxRows != 100_000 || l.MaxLineBytes != 64<<10 {
			t.Errorf("%s: limits = %+v", s.name, l)
		}
		if s.imp.Name() == "" {
			t.Errorf("%s: empty name", s.name)
		}
	}
	if NewHangfire().Name() == NewAgent().Name() {
		t.Error("importer names must differ")
	}
}

func TestConformanceDeterministicAndUniqueIDs(t *testing.T) {
	for _, s := range subjects() {
		data := fixture(t, s.fixture)
		a, repA := mustParse(t, s.imp, data, s.opts(t))
		b, repB := mustParse(t, s.imp, data, s.opts(t))
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(repA, repB) {
			t.Errorf("%s: two parses differ", s.name)
		}
		seen := map[string]bool{}
		for _, e := range a {
			if seen[e.EvidenceID] {
				t.Fatalf("%s: duplicate evidence id %s", s.name, e.EvidenceID)
			}
			seen[e.EvidenceID] = true
		}
		o := s.opts(t)
		o.ImportNonce = "nonce-2"
		c, _ := mustParse(t, s.imp, data, o)
		if len(c) > 0 && c[0].EvidenceID == a[0].EvidenceID {
			t.Errorf("%s: import nonce does not change ids", s.name)
		}
	}
}

func TestConformanceArchivesRefused(t *testing.T) {
	heads := map[string]string{
		"zip": "PK\x03\x04", "gzip": "\x1f\x8b\x08\x00", "7z": "7z\xbc\xaf\x27\x1c",
		"rar": "Rar!\x1a\x07\x00", "xz": "\xfd7zXZ\x00", "bzip2": "BZh91AY", "cab": "MSCF\x00\x00\x00\x00",
	}
	for _, s := range subjects() {
		for kind, head := range heads {
			data := []byte(head + strings.Repeat("\x00", 600))
			_, _, err := parse(s.imp, data, s.opts(t))
			wantQuarantine(t, err, importer.ReasonArchive, "")
			if !strings.Contains(err.Error(), "extract first") {
				t.Errorf("%s/%s: error should say extract first: %v", s.name, kind, err)
			}
		}
	}
}

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xff, 0xfe)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestConformanceEncodingsGiveIdenticalEvents(t *testing.T) {
	for _, s := range subjects() {
		plain := fixture(t, s.fixture)
		want, _ := mustParse(t, s.imp, plain, s.opts(t))
		variants := map[string][]byte{
			"utf-8 bom": append([]byte{0xef, 0xbb, 0xbf}, plain...),
			"utf-16le":  utf16le(string(plain), true),
			"crlf":      bytes.ReplaceAll(plain, []byte("\n"), []byte("\r\n")),
		}
		for name, data := range variants {
			got, _ := mustParse(t, s.imp, data, s.opts(t))
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s/%s: events differ from plain UTF-8 (%d vs %d)", s.name, name, len(got), len(want))
			}
		}
	}
}

func TestConformanceDetectSurvivesEncodings(t *testing.T) {
	for _, s := range subjects() {
		plain := fixture(t, s.fixture)
		want := s.imp.Detect(plain, "x.csv")
		if want == 0 {
			t.Fatalf("%s: fixture not detected", s.name)
		}
		if got := s.imp.Detect(utf16le(string(plain), true), "x.csv"); got != want {
			t.Errorf("%s: utf-16le detect = %d, want %d", s.name, got, want)
		}
		if got := s.imp.Detect(append([]byte{0xef, 0xbb, 0xbf}, plain...), "x.csv"); got != want {
			t.Errorf("%s: bom detect = %d, want %d", s.name, got, want)
		}
	}
}

func TestConformanceSentinelNeverEchoed(t *testing.T) {
	hf := hfHead +
		"job-alpha,default," + sentinel + " * * * *,UTC,,,\n" +
		"job-beta,default,0 * * * *," + sentinel + ",,,\n" +
		"job-gamma,default,0 * * * *,UTC," + sentinel + ",,\n" +
		"job-delta,default,0 * * * *,UTC,,,\n" +
		"job-eps,default,0 0 L * *,UTC,,,\n"
	hfExtra := "JobId,Cron,TimeZoneId,Extra\njob-alpha,0 * * * *,UTC," + sentinel + "\n"
	ag := agHead +
		agRow("job-a", "1", "4", "1", "1", "0", "0", "0", sentinel, "99991231", "30000", "235959") +
		agRow("job-b", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-c", "0", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959")
	cases := []struct {
		imp  importer.Importer
		data string
		o    importer.Options
	}{
		{NewHangfire(), hf, lenient(hangfireOpts())},
		{NewHangfire(), hfExtra, hangfireOpts()},
		{NewAgent(), ag, lenient(agentOpts(t))},
		{NewAgent(), "job_name,freq_type,command\nj,4," + sentinel + "\n", agentOpts(t)},
	}
	for i, c := range cases {
		ev, rep, err := parse(c.imp, []byte(c.data), c.o)
		var all strings.Builder
		fmt.Fprintf(&all, "%+v|%+v|%v|%v", ev, rep, err, rep.KnownGaps)
		if err != nil {
			fmt.Fprintf(&all, "|%s", err.Error())
		}
		if strings.Contains(all.String(), sentinel) {
			t.Errorf("case %d: sentinel leaked into output: %s", i, all.String())
		}
		for _, g := range rep.KnownGaps {
			if err := evidence.LintCausal(g); err != nil {
				t.Errorf("case %d: gap %q: %v", i, g, err)
			}
			if containsFold(g, "strong") || containsFold(g, "proven") {
				t.Errorf("case %d: banned wording in %q", i, g)
			}
		}
	}
}

func TestConformanceCellsKeptVerbatim(t *testing.T) {
	jobs := []string{
		"=HYPERLINK(\"http://x.example.test\")", "+1+1", "-2+3", "@SUM(1,1)",
		"job \"quoted\", with comma", "job\twith tab", "../../etc/job", "<script>alert(1)</script>",
		"job-é中", "'; DROP TABLE jobs;--",
	}
	var hf, ag strings.Builder
	hf.WriteString(hfHead)
	ag.WriteString("job_name,run_date,run_time,run_duration\n")
	for _, j := range jobs {
		q := `"` + strings.ReplaceAll(j, `"`, `""`) + `"`
		hf.WriteString(q + ",default,0 0 1 1 *,UTC,2001-03-24T05:00:00Z,,\n")
		ag.WriteString(q + ",20010324,100000,100\n")
	}
	for _, c := range []struct {
		imp  importer.Importer
		data string
		o    importer.Options
	}{{NewHangfire(), hf.String(), hangfireOpts()}, {NewAgent(), ag.String(), agentOpts(t)}} {
		ev, _ := mustParse(t, c.imp, []byte(c.data), c.o)
		runs := ofKind(ev, KindJobRun)
		if len(runs) != len(jobs) {
			t.Fatalf("%s: runs = %d, want %d", c.imp.Name(), len(runs), len(jobs))
		}
		for i, j := range jobs {
			if runs[i].Attrs["job"] != j {
				t.Errorf("%s: job %d = %q, want %q", c.imp.Name(), i, runs[i].Attrs["job"], j)
			}
		}
	}
}

func TestConformanceDegenerateFiles(t *testing.T) {
	for _, s := range subjects() {
		_, _, err := parse(s.imp, nil, s.opts(t))
		wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
		for _, one := range []string{"x", "\n", "\xef", "\xff", "\"", ","} {
			if _, _, err := parse(s.imp, []byte(one), s.opts(t)); err == nil {
				t.Errorf("%s: %q accepted", s.name, one)
			}
		}
		// Header only: nothing to import, nothing wrong.
		head := hfHead
		if s.name == "agent" {
			head = agHead
		}
		ev, rep, err := parse(s.imp, []byte(head), s.opts(t))
		if err != nil || len(ev) != 0 || rep.Rows != 0 {
			t.Errorf("%s: header only: %v events=%d rows=%d", s.name, err, len(ev), rep.Rows)
		}
	}
}

func TestConformanceTruncationsNeverPanic(t *testing.T) {
	for _, s := range subjects() {
		data := fixture(t, s.fixture)
		for n := 0; n <= len(data); n++ {
			ev, _, _ := parse(s.imp, data[:n], s.opts(t))
			for _, e := range ev {
				if err := e.Validate(s.imp.AllowedAttrs()); err != nil {
					t.Fatalf("%s: prefix %d emitted invalid event: %v", s.name, n, err)
				}
			}
		}
	}
}

func TestConformanceBytesLimit(t *testing.T) {
	for _, s := range subjects() {
		o := s.opts(t)
		o.Limits.MaxBytes = 64
		_, _, err := parse(s.imp, fixture(t, s.fixture), o)
		wantLimit(t, err, "bytes")
		if !errors.Is(err, importer.ErrLimit) {
			t.Errorf("%s: not ErrLimit: %v", s.name, err)
		}
	}
}

func TestConformanceRowLimit(t *testing.T) {
	for _, s := range subjects() {
		o := s.opts(t)
		o.Limits.MaxRows = 2
		_, _, err := parse(s.imp, fixture(t, s.fixture), o)
		wantLimit(t, err, "rows")
	}
}

func TestConformanceContextCancel(t *testing.T) {
	var big strings.Builder
	big.WriteString(hfHead)
	for i := 0; i < 50_000; i++ {
		fmt.Fprintf(&big, "job-%d,default,0 0 * * *,UTC,,,\n", i)
	}
	data := []byte(big.String())

	// Already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _, err := importer.Collect(ctx, NewHangfire(), bytes.NewReader(data), hangfireOpts())
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("pre-cancelled: err=%v after %v", err, time.Since(start))
	}

	// Cancelled from inside the first emit.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	start = time.Now()
	_, err = NewHangfire().Parse(ctx, bytes.NewReader(data), hangfireOpts(), func(evidence.Event) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("mid-parse: err=%v after %v", err, time.Since(start))
	}
}

func TestConformanceEmitErrorStops(t *testing.T) {
	boom := errors.New("sink full")
	for _, s := range subjects() {
		calls := 0
		_, err := s.imp.Parse(context.Background(), bytes.NewReader(fixture(t, s.fixture)), s.opts(t), func(evidence.Event) error {
			calls++
			return boom
		})
		if !errors.Is(err, boom) || calls != 1 {
			t.Errorf("%s: err=%v calls=%d", s.name, err, calls)
		}
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("network attempted: %s", r.URL.Redacted())
	return nil, errors.New("network disabled in tests")
}

func TestConformanceNoNetwork(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}
	defer func() { http.DefaultTransport = old }()
	for _, s := range subjects() {
		mustParse(t, s.imp, fixture(t, s.fixture), s.opts(t))
		s.imp.Detect(fixture(t, s.fixture), "x.csv")
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"hangfire_good.csv", "agent_good.csv"} {
		b := fixture(f, n)
		f.Add(b, n == "agent_good.csv")
		f.Add(b[:len(b)/2], n == "agent_good.csv")
	}
	f.Add([]byte(hfHead+"a,b,* * * * * *,UTC,1,2,3\n"), false)
	f.Add([]byte(agHead+agRow("a", "1", "32", "9", "4", "5", "16", "3", "20010101", "20011231", "0", "235959")), true)
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte, agent bool) {
		o := importer.Options{CaseID: "casecase8888", ImportNonce: "n", FileName: "f.csv",
			Window: importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-24T06:00:00Z")},
			Host:   &importer.HostHint{Label: "h", Role: "sql", TZ: loc}}
		var imp importer.Importer = NewHangfire()
		if agent {
			imp = NewAgent()
		}
		imp.Detect(data, "f.csv")
		ev, rep, err := importer.Collect(context.Background(), imp, bytes.NewReader(data), o)
		for _, e := range ev {
			if verr := e.Validate(imp.AllowedAttrs()); verr != nil {
				t.Fatalf("invalid event: %v", verr)
			}
		}
		if err == nil && rep.Emitted != int64(len(ev)) {
			t.Fatalf("emitted %d but collected %d", rep.Emitted, len(ev))
		}
	})
}
