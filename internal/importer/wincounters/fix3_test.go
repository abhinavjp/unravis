package wincounters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const pdhHead = "\"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)\""

// pdhFile builds a header from counter paths and one row of values per entry in
// rows, at 15 second steps from 2001-10-24 13:00:00.
func pdhFile(paths []string, rows [][]string) []byte {
	var b strings.Builder
	b.WriteString(pdhHead)
	for _, p := range paths {
		b.WriteString(",\"" + p + "\"")
	}
	b.WriteString("\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "\"10/24/2001 13:%02d:%02d.000\"", i/4, (i%4)*15)
		for _, v := range r {
			b.WriteString(",\"" + v + "\"")
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func one(n int) []string {
	r := make([]string, n)
	for i := range r {
		r[i] = "1"
	}
	return r
}

// seriesKeys fails the test if two events of one sample share a series key.
func uniqueEventKeys(t *testing.T, evs []evidence.Event) {
	t.Helper()
	type k struct {
		start time.Time
		key   string
	}
	seen := map[k]bool{}
	for _, e := range evs {
		key := k{e.Start, e.Host + "|" + e.Attrs["object"] + "|" + e.Attrs["counter"] + "|" + e.Attrs["instance"]}
		if seen[key] {
			t.Fatalf("two events under one series key at %v: %q", e.Start, key.key)
		}
		seen[key] = true
	}
}

// H3: with hosts.json supplied, a machine name it does not know is refused. It is
// never filed under the picked host, whose own columns would then share a key.
func TestUnmappedMachineRefusedWhenHostsSupplied(t *testing.T) {
	data := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\DB-B\Memory\Available MBytes`}, [][]string{one(2)})
	o := opts(8, synthetic) // hosts.json knows WEB-A only
	o.Host = &importer.HostHint{Label: "web-a", TZ: synthetic}
	evs, rep, err := parse(t, data, o)
	if !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if len(rep.ObservedHosts) != 2 {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
}

// H3 / M1: without hosts.json the picked host stands for one machine name only.
func TestSeveralMachinesWithoutHostsJSONRefused(t *testing.T) {
	o := importer.Options{CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n",
		Host: &importer.HostHint{Label: "web-a", TZ: synthetic}}
	two := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\DB-B\Memory\Available MBytes`}, [][]string{one(2)})
	if evs, _, err := parse(t, two, o); !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Fatalf("two machines: %d events, %v", len(evs), err)
	}
	// One machine, in whatever letter case, takes the picked host's label.
	same := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\web-a\Memory\Committed Bytes`}, [][]string{one(2)})
	evs, _, err := parse(t, same, o)
	if err != nil || len(evs) != 2 || evs[0].Host != "web-a" || evs[1].Host != "web-a" {
		t.Fatalf("one machine: %d events, %v", len(evs), err)
	}
	uniqueEventKeys(t, evs)
}

// A path with no machine name is the collector's own machine: the picked host,
// also when hosts.json is supplied.
func TestLocalPathsUsePickedHostEvenWithHostsJSON(t *testing.T) {
	data := pdhFile([]string{`\Memory\Available MBytes`}, [][]string{one(1)})
	o := opts(8, synthetic)
	o.Host = &importer.HostHint{Label: "export-box", TZ: synthetic}
	evs, _, err := parse(t, data, o)
	if err != nil || len(evs) != 1 || evs[0].Host != "export-box" {
		t.Fatalf("%d events, %v", len(evs), err)
	}
}

// H3: two columns that resolve to one series key are never merged: the file is
// refused. Two machine names that hosts.json maps to one label reach this rule.
func TestDuplicateSeriesKeyRefused(t *testing.T) {
	o := opts(8, synthetic)
	o.Hosts = func(n string) (importer.HostHint, bool) {
		if strings.HasPrefix(strings.ToLower(n), "web-a") {
			return importer.HostHint{Label: "web-a", TZ: synthetic}, true
		}
		return importer.HostHint{}, false
	}
	data := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\WEB-A.example.test\Memory\Available MBytes`}, [][]string{one(2)})
	evs, _, err := parse(t, data, o)
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || len(evs) != 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	// The same counter listed twice for one machine is refused too.
	dup := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\WEB-A\Memory\Available MBytes`}, [][]string{one(2)})
	if _, _, err := parse(t, dup, opts(8, synthetic)); !errors.As(err, &qe) {
		t.Errorf("duplicate header column: %v", err)
	}
}

// M4: instances that hold database names, server or share names or paths are
// dropped; instances of objects known to be safe stay.
func TestInstanceAllowlist(t *testing.T) {
	paths := []string{
		`\\WEB-A\SQLServer:Query Store(CustomerDb)\Query Store CPU usage`,
		`\\WEB-A\SQLServer:Broker Activation(CustomerDb)\Tasks Started/sec`,
		`\\WEB-A\SQLServer:Database Mirroring(CustomerDb)\Bytes Sent/sec`,
		`\\WEB-A\SQLServer:Columnstore(CustomerDb)\Delta Rowgroups`,
		`\\WEB-A\MSSQL$X:Advanced Analytics(CustomerDb)\Requests`,
		`\\WEB-A\SMB Client Shares(\\fileserver01.example.test\payroll)\Read Bytes/sec`,
		`\\WEB-A\Paging File(\??\C:\pagefile.sys)\% Usage`,
		`\\WEB-A\LogicalDisk(C:\mount\payroll)\Free Megabytes`,
		`\\WEB-A\Web Service(Payroll Site)\Current Connections`,
		`\\WEB-A\SQLServer:Wait Statistics(Average wait time (ms))\Lock waits`,
		`\\WEB-A\Processor(3)\% Processor Time`,
		`\\WEB-A\LogicalDisk(C:)\Free Megabytes`,
		`\\WEB-A\Paging File(_Total)\% Usage`,
	}
	evs, rep, err := parse(t, pdhFile(paths, [][]string{one(len(paths))}), opts(8, synthetic))
	if err != nil || len(evs) != len(paths) {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	kept := map[string]string{}
	for _, e := range evs {
		for k, v := range e.Attrs {
			for _, bad := range []string{"CustomerDb", "fileserver01", "payroll", "pagefile", "Payroll", "mount"} {
				if strings.Contains(v, bad) {
					t.Errorf("attr %s=%q still holds a name or path", k, v)
				}
			}
		}
		if in, has := e.Attrs["instance"]; has {
			kept[e.Attrs["object"]+"\\"+e.Attrs["counter"]] = in
		}
	}
	for key, want := range map[string]string{
		`SQLServer:Wait Statistics\Lock waits`: "Average wait time (ms)",
		`Processor\% Processor Time`:           "3",
		`LogicalDisk\Free Megabytes`:           "C:",
		`Paging File\% Usage`:                  "_Total",
	} {
		if kept[key] != want {
			t.Errorf("%s: instance %q, want %q", key, kept[key], want)
		}
	}
	if len(kept) != 4 {
		t.Errorf("instances kept %v", kept)
	}
	if !hasGap(rep, GapInstancesDropped) {
		t.Errorf("no gap for dropped instances: %v", rep.KnownGaps)
	}
	uniqueEventKeys(t, evs)
}

// M4 / H3: when dropping instance names would put several columns under one key,
// they are told apart by a number in header order, never merged.
func TestDroppedInstancesAreNumbered(t *testing.T) {
	paths := []string{
		`\\WEB-A\SQLServer:Databases(db-a)\Transactions/sec`,
		`\\WEB-A\SQLServer:Databases(db-b)\Transactions/sec`,
		`\\WEB-A\SQLServer:Databases(db-c)\Log Flushes/sec`, // alone: no number
	}
	evs, rep, err := parse(t, pdhFile(paths, [][]string{{"1", "2", "3"}}), opts(8, synthetic))
	if err != nil || len(evs) != 3 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	uniqueEventKeys(t, evs)
	got := []string{evs[0].Attrs["instance"], evs[1].Attrs["instance"], evs[2].Attrs["instance"]}
	if got[0] != "instance-1" || got[1] != "instance-2" || got[2] != "" {
		t.Errorf("instances %q", got)
	}
	if _, has := evs[2].Attrs["instance"]; has {
		t.Error("a lone dropped instance should have no instance attribute")
	}
	for _, e := range evs {
		if strings.Contains(fmt.Sprint(e.Attrs), "db-") {
			t.Errorf("database name kept: %v", e.Attrs)
		}
	}
	if !hasGap(rep, GapInstancesNumbered) {
		t.Errorf("no numbering gap: %v", rep.KnownGaps)
	}
}

// M9: rows x columns is capped. A file inside MaxBytes and MaxRows still refuses
// with a LimitError once its events pass MaxEvents, and Parse emits nothing
// beyond the cap.
func TestMaxEventsLimit(t *testing.T) {
	data := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`, `\\WEB-A\Memory\Committed Bytes`}, [][]string{one(2), one(2), one(2)})
	o := opts(8, synthetic)
	o.Limits = importer.Limits{MaxEvents: 5}
	var n int
	rep, err := New().Parse(context.Background(), bytes.NewReader(data), o, func(evidence.Event) error { n++; return nil })
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "events" || le.Max != 5 || !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("got %v", err)
	}
	if n > 5 || rep.Emitted > 5 {
		t.Errorf("emitted %d (reported %d) past the cap of 5", n, rep.Emitted)
	}
	if evs, _, err := parse(t, data, o); err == nil || len(evs) != 0 {
		t.Errorf("Collect returned %d events with err %v", len(evs), err)
	}
	o.Limits = importer.Limits{MaxEvents: 6}
	if evs, _, err := parse(t, data, o); err != nil || len(evs) != 6 {
		t.Errorf("exactly at the cap: %d, %v", len(evs), err)
	}
	if New().DefaultLimits().MaxEvents <= 0 {
		t.Error("no default event cap")
	}
}

// M8: a zero or far-off date rejects its row; it does not abort the import.
func TestInsaneTimesRejectTheRow(t *testing.T) {
	var b strings.Builder
	b.WriteString(pdhHead + ",\"\\\\WEB-A\\Memory\\Available MBytes\"\n")
	b.WriteString("\"10/24/2001 13:00:00.000\",\"1\"\n")
	b.WriteString("\"01/01/0001 00:00:00.000\",\"2\"\n")
	b.WriteString("\"01/01/1601 00:00:00.000\",\"3\"\n")
	b.WriteString("\"10/24/2001 13:01:00.000\",\"4\"\n")
	o := opts(8, synthetic)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	evs, rep, err := parse(t, []byte(b.String()), o)
	if err != nil || len(evs) != 2 || rep.Rejected != 2 {
		t.Fatalf("%d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
	for _, re := range rep.Errors {
		if re.Reason != importer.ReasonBadTime {
			t.Errorf("reason %v", re)
		}
	}
}

// M2: every fixed sentence the package ships passes the causal-wording lint.
func TestGapSentencesPassLintCausal(t *testing.T) {
	for _, g := range []string{GapFixedBias, GapHostCPU, GapNoCores, GapLegacyProc, GapBadColumns,
		GapDateOrder, GapInstancesDropped, GapInstancesNumbered} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

// Low: the date order is assumed, and the report says so.
func TestDateOrderGapReported(t *testing.T) {
	_, rep, err := parse(t, fixture(t, "counters_basic.csv"), opts(8, synthetic))
	if err != nil || !hasGap(rep, GapDateOrder) {
		t.Fatalf("%v %v", err, rep.KnownGaps)
	}
}

// Low: each event owns its attribute map; a consumer that changes one event's
// attributes does not change the next event of the same column.
func TestEventsDoNotShareAttrMaps(t *testing.T) {
	data := pdhFile([]string{`\\WEB-A\Memory\Available MBytes`}, [][]string{one(1), one(1), one(1)})
	var seen []string
	_, err := New().Parse(context.Background(), bytes.NewReader(data), opts(8, synthetic), func(e evidence.Event) error {
		seen = append(seen, e.Attrs["counter"])
		e.Attrs["counter"] = "changed"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range seen {
		if s != "Available MBytes" {
			t.Errorf("event %d saw %q: attrs are shared", i, s)
		}
	}
}
