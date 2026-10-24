package sqlserver

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
	"api-audit-explorer/internal/importer/wincounters"
)

// pdhFixtures: every attribute is a part of a counter path in the header
// (object, counter, instance), never a whole free-text cell, so NoFreeText
// applies, as for win-counters. The time comes from the header bias (-720,
// NZST), which matches sql-a's Auckland zone in June 2001.
func pdhFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       readFixture(t, "pdh_good.csv"),
		Bad:        readFixture(t, "pdh_bad.csv"),
		NoFreeText: true,
		Options:    importer.Options{Host: sqlA(t), FileName: "sql-a-counters.csv"},
	}
}

func TestPDHConformance(t *testing.T) { importertest.Run(t, NewPDH(), pdhFixtures(t)) }

func utf16le(s string) []byte {
	b := []byte{0xFF, 0xFE}
	for _, r := range s { // ASCII only
		b = append(b, byte(r), 0)
	}
	return b
}

func TestPDHDetect(t *testing.T) {
	sqlHead := `"(PDH-CSV 4.0) (New Zealand Standard Time)(-720)","\\SQL-A\SQLServer:General Statistics\Processes blocked"` + "\r\n"
	cases := map[string]struct {
		head []byte
		want int
	}{
		"default instance":   {[]byte(sqlHead), 100},
		"named instance":     {[]byte(`"(PDH-CSV 4.0) (New Zealand Standard Time)(-720)","\\SQL-A\MSSQL$INST1:Locks(_Total)\Lock Waits/sec"` + "\n"), 100},
		"utf-16le":           {utf16le(sqlHead), 100},
		"good fixture":       {readFixture(t, "pdh_good.csv"), 100},
		"no sql counters":    {[]byte(`"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)","\\WEB-A\Processor(_Total)\% Processor Time"` + "\n" + `"10/24/2001 13:00:00.000","\\SQL-A\SQLServer:x\y"` + "\n"), 0},
		"not pdh":            {readFixture(t, "xe_good.csv"), 0},
		"sql words, not pdh": {[]byte(`"time","\\SQL-A\SQLServer:General Statistics\Processes blocked"` + "\n"), 0},
	}
	imp := NewPDH()
	for name, c := range cases {
		if got := imp.Detect(c.head, "fixture.csv"); got != c.want {
			t.Errorf("%s: Detect = %d, want %d", name, got, c.want)
		}
	}
	// A SQL file is the same parser under two names: the registry must list
	// both rather than silently pick one.
	reg := importer.NewRegistry(wincounters.New(), imp)
	best, _, tied := reg.Detect([]byte(sqlHead), "fixture.csv")
	if best == nil || best.Name() != PDHName || strings.Join(tied, ",") != PDHName+","+wincounters.Name {
		t.Errorf("registry on a SQL PDH file: best %v, tied %v", best, tied)
	}
	best, _, tied = reg.Detect(cases["no sql counters"].head, "fixture.csv")
	if best == nil || best.Name() != wincounters.Name || tied != nil {
		t.Errorf("registry on a non-SQL PDH file: best %v, tied %v", best, tied)
	}
}

func TestPDHEvents(t *testing.T) {
	evs, rep, err := collect(t, NewPDH(), readFixture(t, "pdh_good.csv"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Importer != PDHName || rep.Emitted != int64(len(evs)) || len(evs) == 0 {
		t.Fatalf("report importer %q, emitted %d, events %d", rep.Importer, rep.Emitted, len(evs))
	}
	var blocked []evidence.Event
	for _, e := range evs {
		if e.Source != PDHName || e.Host != "sql-a" || e.Kind != "counter" {
			t.Fatalf("event %+v: want source %s, host sql-a, kind counter", e, PDHName)
		}
		switch e.Attrs["counter"] {
		case "Processes blocked":
			if *e.Value > 0 {
				blocked = append(blocked, e)
			}
		case "Transactions/sec":
			if _, ok := e.Attrs["instance"]; ok {
				t.Errorf("database instance kept: %v", e.Attrs)
			}
		case "Lock waits":
			if e.Attrs["instance"] != "Average wait time (ms)" {
				t.Errorf("wait statistics instance = %q", e.Attrs["instance"])
			}
		case "% Processor Time":
			if e.Unit != "percent-host" {
				t.Errorf("host CPU unit = %q, want percent-host", e.Unit)
			}
		}
		for _, v := range e.Attrs {
			if strings.Contains(v, "db-a") || strings.Contains(strings.ToLower(v), "sql-a") {
				t.Errorf("attribute leaks a database or host name: %v", e.Attrs)
			}
		}
	}
	// 14:00:15 and 14:00:30 NZST (UTC+12) are 02:00:15Z and 02:00:30Z.
	want := []time.Time{time.Date(2001, 6, 15, 2, 0, 15, 0, time.UTC), time.Date(2001, 6, 15, 2, 0, 30, 0, time.UTC)}
	if len(blocked) != 2 || !blocked[0].Start.Equal(want[0]) || !blocked[1].Start.Equal(want[1]) {
		t.Errorf("blocked samples = %+v, want two at %v", blocked, want)
	}
	if !contains(rep.KnownGaps, wincounters.GapFixedBias) || !contains(rep.KnownGaps, wincounters.GapHostCPU) {
		t.Errorf("gaps = %v", rep.KnownGaps)
	}
}

// A host whose zone disagrees with the header bias is refused, as in win-counters.
func TestPDHWrongHostZoneRefused(t *testing.T) {
	o := testOpts(t)
	o.Host.TZ = time.UTC
	_, _, err := collect(t, NewPDH(), readFixture(t, "pdh_good.csv"), o)
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonTZMismatch {
		t.Fatalf("UTC host with bias -720: got %v, want tz-mismatch", err)
	}
}

// pdhSentinelScrub hides from importertest.FuzzParse's sentinel check what a
// mutated header can legitimately carry, as win-counters' own fuzz does: a raw
// name in Report.ObservedHosts (raw by design) and an attribute that is a part
// of a counter path. Everything else every event and report carries is still
// checked, and Run checks Fixtures.Bad for attribute leaks.
type pdhSentinelScrub struct{ importer.Importer }

func (s pdhSentinelScrub) Parse(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rep, err := s.Importer.Parse(ctx, r, o, func(e evidence.Event) error {
		clean := make(map[string]string, len(e.Attrs))
		for k, v := range e.Attrs {
			if !strings.Contains(v, importertest.Sentinel) {
				clean[k] = v
			}
		}
		e.Attrs = clean
		return emit(e)
	})
	kept := rep.ObservedHosts[:0:0]
	for _, h := range rep.ObservedHosts {
		if !strings.Contains(h, importertest.Sentinel) {
			kept = append(kept, h)
		}
	}
	rep.ObservedHosts = kept
	return rep, err
}

func FuzzParsePDH(f *testing.F) {
	f.Add(readFixture(f, "golden/pdh.csv"))
	f.Add(utf16le(string(readFixture(f, "pdh_good.csv"))))
	importertest.FuzzParse(f, pdhSentinelScrub{NewPDH()}, pdhFixtures(f))
}
