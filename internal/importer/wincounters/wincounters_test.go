package wincounters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

var synthetic = time.FixedZone("synthetic", 2*3600) // matches bias -120

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts(cores int, tz *time.Location) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Hosts: func(o string) (importer.HostHint, bool) {
			if strings.EqualFold(o, "WEB-A") {
				return importer.HostHint{Label: "web-a", Role: "iis", TZ: tz, LogicalCores: cores}, true
			}
			return importer.HostHint{}, false
		},
	}
}

func parse(t testing.TB, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), New(), bytes.NewReader(data), o)
}

// fixtures: every attribute is derived from a counter path in the header (object,
// counter, instance), none is a whole free-text cell, so NoFreeText applies. A
// path with spreadsheet-trigger characters is covered by
// TestSpecialCellsKeptVerbatim.
func fixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, "counters_basic.csv"),
		Bad:        fixture(t, "bad_sentinel.csv"),
		Options:    opts(8, synthetic),
		NoFreeText: true,
	}
}

func TestConformance(t *testing.T) {
	importertest.Run(t, New(), fixtures(t))
}

func TestBasicWideToLong(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "counters_basic.csv"), opts(8, synthetic))
	if err != nil {
		t.Fatal(err)
	}
	// 10 rows x 3 columns, minus 7 holes (" ") = 23 events.
	if len(evs) != 23 || rep.Rows != 10 || rep.Rejected != 0 || rep.Emitted != 23 {
		t.Fatalf("events %d rows %d rejected %d emitted %d", len(evs), rep.Rows, rep.Rejected, rep.Emitted)
	}
	want := time.Date(2001, 10, 24, 11, 0, 0, 0, time.UTC) // 13:00 at bias -120
	if !evs[0].Start.Equal(want) || evs[0].Start.Location() != time.UTC || !evs[0].End.Equal(evs[0].Start) {
		t.Errorf("first start %v", evs[0].Start)
	}
	e := evs[0]
	if e.Kind != "counter" || e.Host != "web-a" || e.TZRule != clock.RuleHeaderBias || e.Source != Name {
		t.Errorf("event fields %+v", e)
	}
	if e.Unit != "percent-host" || *e.Value != 12.5 || e.Attrs["instance"] != "_Total" || e.Attrs["counter"] != "% Processor Time" {
		t.Errorf("cpu event %+v", e)
	}
	if rep.Quality.TimezoneRule != clock.RuleHeaderBias || rep.FieldRules["timestamp"] != clock.RuleHeaderBias {
		t.Errorf("tz rule not reported: %+v %+v", rep.Quality, rep.FieldRules)
	}
	if rep.Quality.SourceVersion != "PDH-CSV 4.0" || rep.Quality.CoverageFirst == nil || !rep.Quality.CoverageFirst.Equal(want) {
		t.Errorf("quality %+v", rep.Quality)
	}
	if got := strings.Join(rep.KnownGaps, "|"); !strings.Contains(got, GapFixedBias) || !strings.Contains(got, GapHostCPU) {
		t.Errorf("gaps %q", got)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-A" {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
	for _, e := range evs {
		// 13:00:30 processor sample is a hole: missing, not 0.
		if e.Attrs["instance"] == "_Total" && e.Start.Equal(want.Add(30*time.Second)) {
			t.Error("hole emitted as a sample")
		}
		if e.Value == nil {
			t.Fatal("nil value")
		}
	}
}

func TestProcessV2Division(t *testing.T) {
	t.Run("divided by cores", func(t *testing.T) {
		evs, rep, err := parse(t, fixture(t, "counters_processv2.csv"), opts(8, synthetic))
		if err != nil {
			t.Fatal(err)
		}
		var sql []float64
		for _, e := range evs {
			if e.Attrs["object"] == "Process" {
				t.Fatal("legacy Process counter kept although Process V2 is present")
			}
			if e.Attrs["instance"] == "sqlservr:4242" {
				if e.Unit != "percent-host" {
					t.Errorf("unit %q", e.Unit)
				}
				sql = append(sql, *e.Value)
			}
			if e.Attrs["object"] == "SQLServer:Databases" {
				if _, has := e.Attrs["instance"]; has {
					t.Error("database instance name kept")
				}
			}
		}
		if fmt.Sprint(sql) != "[10 20 30 5]" {
			t.Errorf("sqlservr values %v", sql)
		}
		if !hasGap(rep, GapLegacyProc) || hasGap(rep, GapNoCores) {
			t.Errorf("gaps %v", rep.KnownGaps)
		}
	})
	t.Run("unknown cores", func(t *testing.T) {
		evs, rep, err := parse(t, fixture(t, "counters_processv2.csv"), opts(0, synthetic))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Attrs["instance"] == "sqlservr:4242" && (e.Unit != "percent-one-core" || *e.Value < 40) {
				t.Errorf("undivided value expected: %+v", e)
			}
		}
		if !hasGap(rep, GapNoCores) {
			t.Errorf("missing gap: %v", rep.KnownGaps)
		}
	})
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

func TestBiasMismatchQuarantines(t *testing.T) {
	var n int
	o := opts(8, time.FixedZone("wrong", 3600))
	rep, err := New().Parse(context.Background(), bytes.NewReader(fixture(t, "bad_bias_mismatch.csv")), o,
		func(evidence.Event) error { n++; return nil })
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonTZMismatch || !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("got %v", err)
	}
	if n != 0 || rep.Emitted != 0 {
		t.Errorf("emitted %d before quarantine", n)
	}
	// Without a host zone the header bias is the rule and the file is accepted.
	if _, _, err := parse(t, fixture(t, "bad_bias_mismatch.csv"), opts(8, nil)); err != nil {
		t.Errorf("nil host TZ: %v", err)
	}
}

func TestUnknownHost(t *testing.T) {
	o := opts(8, synthetic)
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	_, rep, err := parse(t, fixture(t, "counters_basic.csv"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("got %v", err)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-A" {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
}

func TestPickedHostForLocalPaths(t *testing.T) {
	csvText := "\"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)\",\"\\Processor(_Total)\\% Processor Time\"\n" +
		"\"10/24/2001 13:00:00.000\",\"7\"\n"
	o := importer.Options{CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n",
		Host: &importer.HostHint{Label: "web-a", TZ: synthetic}}
	evs, rep, err := parse(t, []byte(csvText), o)
	if err != nil || len(evs) != 1 || evs[0].Host != "web-a" || len(rep.ObservedHosts) != 0 {
		t.Fatalf("evs %d err %v observed %v", len(evs), err, rep.ObservedHosts)
	}
	o.Host = nil
	if _, _, err := parse(t, []byte(csvText), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host at all: %v", err)
	}
}

func TestHeaderProblems(t *testing.T) {
	t.Run("missing bias", func(t *testing.T) {
		_, _, err := parse(t, []byte("\"(PDH-CSV 4.0) (Synthetic Standard Time)\",\"\\\\WEB-A\\Memory\\Available MBytes\"\n"), opts(8, synthetic))
		var qe *importer.QuarantineError
		if !errors.Is(err, importer.ErrTimezoneUnlabelled) || !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("5000 columns", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("\"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)\"")
		for i := 0; i < 5000; i++ {
			fmt.Fprintf(&b, ",\\M\\C%d", i) // short, unquoted: stays under the line cap
		}
		b.WriteString("\n")
		o := opts(8, synthetic)
		o.Host = &importer.HostHint{Label: "web-a"}
		_, _, err := parse(t, []byte(b.String()), o)
		var le *importer.LimitError
		if !errors.As(err, &le) || le.Limit != "fields" || !errors.Is(err, importer.ErrLimit) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("binary logs", func(t *testing.T) {
		for name, data := range map[string][]byte{
			"blg":  append([]byte("PERF\x01\x00\x00\x00"), make([]byte, 64)...),
			"evtx": append([]byte("ElfFile\x00"), make([]byte, 64)...),
		} {
			want := map[string]importer.Reason{"evtx": importer.ReasonBinaryEVTX, "blg": importer.ReasonBinaryBLG}[name]
			_, _, err := parse(t, data, opts(8, synthetic))
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) || qe.Reason != want {
				t.Errorf("%s: got %v", name, err)
			}
		}
	})
	t.Run("not a counters csv", func(t *testing.T) {
		_, _, err := parse(t, []byte("a,b\n1,2\n"), opts(8, synthetic))
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
			t.Errorf("got %v", err)
		}
	})
}

func TestRejectsAndFraction(t *testing.T) {
	build := func(bad int) []byte {
		var b strings.Builder
		b.WriteString("\"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)\",\"\\\\WEB-A\\Memory\\Available MBytes\"\n")
		for i := 0; i < 30; i++ {
			v := "100"
			if i < bad {
				v = "oops"
			}
			fmt.Fprintf(&b, "\"10/24/2001 13:%02d:00.000\",\"%s\"\n", i, v)
		}
		return []byte(b.String())
	}
	evs, rep, err := parse(t, build(1), opts(8, synthetic))
	if err != nil || len(evs) != 29 || rep.Rejected != 1 || len(rep.Errors) != 1 ||
		rep.Errors[0].Reason != importer.ReasonBadNumber || rep.Errors[0].Line != 2 {
		t.Fatalf("1 bad: %d events, %+v, %v", len(evs), rep, err)
	}
	_, _, err = parse(t, build(2), opts(8, synthetic))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction {
		t.Fatalf("2 bad: %v", err)
	}
}

func TestSpecialCellsKeptVerbatim(t *testing.T) {
	instances := []string{"=cmd+1:9", "+sum:9", "-neg:9", "@at:9", "\tTab:9", "\rCR:9"}
	var b strings.Builder
	b.WriteString("\"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)\"")
	for _, in := range instances {
		fmt.Fprintf(&b, ",\"\\\\WEB-A\\Process V2(%s)\\Handle Count\"", in)
	}
	b.WriteString("\n\"10/24/2001 13:00:00.000\"")
	for range instances {
		b.WriteString(",\"3\"")
	}
	b.WriteString("\n")
	evs, _, err := parse(t, []byte(b.String()), opts(8, synthetic))
	if err != nil || len(evs) != len(instances) {
		t.Fatalf("%d events, err %v", len(evs), err)
	}
	for i, e := range evs {
		// encoding/csv folds a bare CR inside a quoted field to LF-less text only when
		// paired with LF, so the CR instance stays as given.
		if e.Attrs["instance"] != instances[i] {
			t.Errorf("instance %d: %q != %q", i, e.Attrs["instance"], instances[i])
		}
		if err := e.Validate(New().AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
}

func TestNewImporterForP3(t *testing.T) {
	called := false
	imp := NewImporter("sql-pdh", func(h []byte) int { called = true; return 42 })
	if imp.Name() != "sql-pdh" || imp.Detect([]byte("x"), "f.csv") != 42 || !called {
		t.Error("custom name or detect ignored")
	}
	evs, _, err := importer.Collect(context.Background(), imp, bytes.NewReader(fixture(t, "counters_basic.csv")), opts(8, synthetic))
	if err != nil || len(evs) == 0 || evs[0].Source != "sql-pdh" || !strings.Contains(evs[0].EvidenceID, ":sql-pdh:") {
		t.Fatalf("source not renamed: %v", err)
	}
	if NewImporter("x", nil).Detect(fixture(t, "counters_basic.csv"), "") != 95 {
		t.Error("nil detect should fall back to DetectPDH")
	}
}

func TestDetect(t *testing.T) {
	good := fixture(t, "counters_basic.csv")
	if DetectPDH(good) != 95 || DetectPDH([]byte("TimeCreatedUtc,MachineName\n")) != 0 || DetectPDH(nil) != 0 {
		t.Error("detect scores")
	}
	u16 := []byte{0xFF, 0xFE}
	for _, r := range string(good[:80]) {
		u16 = append(u16, byte(r), 0)
	}
	if DetectPDH(u16) != 95 {
		t.Error("utf-16le head not detected")
	}
}

type failTransport struct{ hit *bool }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*f.hit = true
	return nil, errors.New("network is off in importer tests")
}

func TestNoNetwork(t *testing.T) {
	var hit bool
	old := http.DefaultTransport
	http.DefaultTransport = failTransport{&hit}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(t, fixture(t, "counters_basic.csv"), opts(8, synthetic)); err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("importer used the network")
	}
}

func TestErrorsCarryNoContent(t *testing.T) {
	_, rep, err := parse(t, fixture(t, "bad_sentinel.csv"), opts(8, synthetic))
	if err == nil {
		t.Fatal("expected quarantine on 3 of 5 rows rejected")
	}
	if strings.Contains(fmt.Sprintf("%v %+v", err, rep), "SENTINEL") {
		t.Error("content in error or report")
	}
	reasons := map[importer.Reason]bool{}
	for _, re := range rep.Errors {
		reasons[re.Reason] = true
	}
	for _, r := range []importer.Reason{importer.ReasonBadNumber, importer.ReasonBadTime, importer.ReasonFieldCount} {
		if !reasons[r] {
			t.Errorf("missing reason %s in %v", r, rep.Errors)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"counters_basic.csv", "counters_processv2.csv", "bad_sentinel.csv", "bad_bias_mismatch.csv"} {
		f.Add(fixture(f, n))
	}
	f.Add([]byte("\xff\xfe(\x00P\x00"))
	fx := fixtures(f)
	fx.KeepsCellValues = true // instances and counter names are whole header-cell parts kept as attributes
	importertest.FuzzParse(f, New(), fx)
}
