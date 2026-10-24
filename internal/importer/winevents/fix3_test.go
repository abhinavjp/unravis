package winevents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const utcHeader = "\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\"\n"
const localHeader = "\"TimeCreated\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\"\n"

func row(ts, machine, id string) string {
	return "\"" + ts + "\",\"" + machine + "\",\"System\",\"P\",\"" + id + "\",\"4\"\n"
}

// zone13 stands in for a UTC+13 host (web-a in the review's example).
var zone13 = time.FixedZone("synthetic+13", 13*3600)

// withPicked returns opts(tz) with a picked host, as the local-time rule needs.
func withPicked(tz *time.Location) importer.Options {
	o := opts(tz)
	o.Host = &importer.HostHint{Label: "web-a", Role: "iis", TZ: tz}
	return o
}

// H2: a MachineName that hosts.json does not map is refused, even when a host
// was picked. It is never filed under the picked host.
func TestUnmappedMachineRefusedWhenHostsSupplied(t *testing.T) {
	data := utcHeader + row("2001-03-01T10:00:00Z", "WEB-A", "1") + row("2001-03-01T10:00:00Z", "DB-B", "2")
	o := opts(nil) // Hosts maps WEB-A only
	o.Host = &importer.HostHint{Label: "web-a"}
	evs, rep, err := parse(t, New(), []byte(data), o)
	if !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Fatalf("got %d events, err %v", len(evs), err)
	}
	if len(rep.ObservedHosts) != 2 {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
	// An empty MachineName cannot map either.
	data = utcHeader + row("2001-03-01T10:00:00Z", "", "1")
	if _, _, err := parse(t, New(), []byte(data), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("empty machine name with hosts.json: %v", err)
	}
}

// M1: without hosts.json the picked host stands for exactly one machine name.
func TestSeveralMachinesWithoutHostsJSONRefused(t *testing.T) {
	o := importer.Options{CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n",
		Host: &importer.HostHint{Label: "web-a"}}
	two := utcHeader + row("2001-03-01T10:00:00Z", "WEB-A", "1") + row("2001-03-01T10:00:00Z", "DB-B", "2")
	evs, _, err := parse(t, New(), []byte(two), o)
	if !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Fatalf("two machines: %d events, %v", len(evs), err)
	}
	// One machine, in any letter case, is fine and takes the picked host's label.
	one := utcHeader + row("2001-03-01T10:00:00Z", "WEB-A", "1") + row("2001-03-01T10:01:00Z", "web-a", "2")
	evs, _, err = parse(t, New(), []byte(one), o)
	if err != nil || len(evs) != 2 || evs[0].Host != "web-a" || evs[1].Host != "web-a" {
		t.Fatalf("one machine: %d events, %v", len(evs), err)
	}
	// No host at all is still a refusal.
	o.Host = nil
	if _, _, err := parse(t, New(), []byte(one), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host: %v", err)
	}
}

// H2: a local TimeCreated is in the exporting machine's zone. The zone is the
// picked host's only; hosts.json's zone for MachineName is not used.
func TestLocalTimeUsesPickedHostZoneOnly(t *testing.T) {
	data := localHeader + row("2001-03-01T10:00:00", "WEB-A", "1")
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata")
	}
	// hosts.json says web-a is in Los Angeles; the picked (exporting) host is +13.
	o := opts(la)
	o.Host = &importer.HostHint{Label: "export-box", TZ: zone13}
	evs, rep, err := parse(t, New(), []byte(data), o)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if want := time.Date(2001, 2, 28, 21, 0, 0, 0, time.UTC); !evs[0].Start.Equal(want) {
		t.Errorf("start %v, want %v (picked host zone)", evs[0].Start, want)
	}
	if evs[0].Host != "web-a" {
		t.Errorf("host label %q: MachineName still maps through hosts.json", evs[0].Host)
	}
	if !hasGap(rep, GapLocalExporterZone) {
		t.Errorf("exporter-zone gap missing: %v", rep.KnownGaps)
	}
}

func TestLocalTimeNeedsPickedHostZone(t *testing.T) {
	data := localHeader + row("2001-03-01T10:00:00", "WEB-A", "1")
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata")
	}
	for name, o := range map[string]importer.Options{
		"hosts.json zone only": opts(la),
		"picked host no zone":  withPicked(nil),
	} {
		var n int
		_, err := New().Parse(context.Background(), strings.NewReader(data), o, func(evidence.Event) error { n++; return nil })
		var qe *importer.QuarantineError
		if !errors.Is(err, importer.ErrTimezoneUnlabelled) || !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled || n != 0 {
			t.Errorf("%s: %d events, %v", name, n, err)
		}
	}
	// A header-only file is refused up front too, before any row is read.
	if _, err := New().Parse(context.Background(), strings.NewReader(localHeader), opts(la), func(evidence.Event) error { return nil }); !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Errorf("header only: %v", err)
	}
}

// Low: a raw TimeCreated written in the exporting machine's culture gets one
// clear refusal with a hint, not "too many rows rejected".
func TestCultureFormatTimeRefusedWithHint(t *testing.T) {
	for name, data := range map[string]string{
		"local column": localHeader + row("3/1/2001 10:00:00 AM", "WEB-A", "1") + row("3/1/2001 10:01:00 AM", "WEB-A", "2"),
		"utc column":   utcHeader + row("01/03/2001 10:00:00", "WEB-A", "1") + row("01/03/2001 10:01:00", "WEB-A", "2"),
	} {
		_, rep, err := parse(t, New(), []byte(data), withPicked(zone13))
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonBadTime || (qe.Column != "TimeCreated" && qe.Column != "TimeCreatedUtc") {
			t.Fatalf("%s: got %v", name, err)
		}
		if errors.Is(err, importer.ErrLimit) || (qe.Reason == importer.ReasonRejectFraction) {
			t.Errorf("%s: still the reject-fraction refusal", name)
		}
		if !hasGap(rep, GapTimeFormat) {
			t.Errorf("%s: no hint in the report: %v", name, rep.KnownGaps)
		}
		if strings.Contains(err.Error(), "AM") || strings.Contains(strings.Join(rep.KnownGaps, "|"), "2001") {
			t.Errorf("%s: cell content in the refusal", name)
		}
	}
	// Garbage on a later row stays a per-row reject.
	data := localHeader + row("2001-03-01T10:00:00", "WEB-A", "1") + row("3/1/2001 10:00:00 AM", "WEB-A", "2")
	o := withPicked(zone13)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	evs, rep, err := parse(t, New(), []byte(data), o)
	if err != nil || len(evs) != 1 || rep.Rejected != 1 {
		t.Errorf("later culture row: %d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
}

// Low: an unquoted data row whose first cell starts with '#' is a row, counted.
func TestHashRowIsCounted(t *testing.T) {
	data := "#TYPE Selected.Synthetic\n" + utcHeader +
		row("2001-03-01T10:00:00Z", "WEB-A", "1") +
		"#2001-03-01T10:01:00Z,WEB-A,System,P,2,4\n"
	o := opts(nil)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	evs, rep, err := parse(t, New(), []byte(data), o)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if rep.Rows != 2 || rep.Rejected != 1 || rep.Errors[0].Line != 4 || rep.Errors[0].Reason != importer.ReasonBadTime {
		t.Errorf("rows %d rejected %d errors %v", rep.Rows, rep.Rejected, rep.Errors)
	}
	// Event line numbers still match the file with its #TYPE line.
	if evs[0].Line != 3 {
		t.Errorf("line %d, want 3", evs[0].Line)
	}
}

// Low: Task Scheduler event 111 is "terminated", not a user request; 330 is the
// user stop.
func TestTaskSchedulerStopIDs(t *testing.T) {
	const prov = "Microsoft-Windows-TaskScheduler"
	kind, attrs, ok := ClassifyTaskScheduler(prov, 111)
	if !ok || kind != "task-stop" || attrs["reason"] != "" {
		t.Errorf("111: %q %v %v", kind, attrs, ok)
	}
	kind, attrs, ok = ClassifyTaskScheduler(prov, 330)
	if !ok || kind != "task-stop" || attrs["reason"] != "user-request" {
		t.Errorf("330: %q %v %v", kind, attrs, ok)
	}
	if _, attrs, _ := ClassifyTaskScheduler(prov, 329); attrs["reason"] != "timeout" {
		t.Errorf("329: %v", attrs)
	}
}

// M9: the importer refuses a file past the event cap, as a LimitError.
func TestMaxEventsLimit(t *testing.T) {
	o := opts(nil)
	o.Limits = importer.Limits{MaxEvents: 2}
	evs, rep, err := parse(t, New(), fixture(t, "events_utc.csv"), o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "events" || le.Max != 2 || len(evs) != 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if rep.Emitted != 2 {
		t.Errorf("emitted %d before the refusal, want 2", rep.Emitted)
	}
	o.Limits = importer.Limits{MaxEvents: 5}
	if evs, _, err := parse(t, New(), fixture(t, "events_utc.csv"), o); err != nil || len(evs) != 5 {
		t.Errorf("exactly at the cap: %d, %v", len(evs), err)
	}
	if New().DefaultLimits().MaxEvents <= 0 {
		t.Error("no default event cap")
	}
}

// M8: one out-of-range date rejects its row; it does not abort the import.
func TestInsaneTimesRejectTheRow(t *testing.T) {
	data := utcHeader + row("2001-03-01T10:00:00Z", "WEB-A", "1") + row("0001-01-01T00:00:00Z", "WEB-A", "2") +
		row("1601-01-01T00:00:00Z", "WEB-A", "3") + row("2001-03-01T10:02:00Z", "WEB-A", "4")
	o := opts(nil)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	evs, rep, err := parse(t, New(), []byte(data), o)
	if err != nil || len(evs) != 2 || rep.Rejected != 2 {
		t.Fatalf("%d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
	for _, re := range rep.Errors {
		if re.Reason != importer.ReasonBadTime {
			t.Errorf("reason %v", re)
		}
	}
	local := localHeader + row("2001-03-01T10:00:00", "WEB-A", "1") + row("0001-01-01T00:00:00", "WEB-A", "2")
	po := withPicked(zone13)
	po.Limits = importer.Limits{MaxRejectFraction: 1}
	if evs, rep, err := parse(t, New(), []byte(local), po); err != nil || len(evs) != 1 || rep.Rejected != 1 {
		t.Errorf("local: %d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
}

// M2: every fixed sentence the package ships passes the causal-wording lint.
func TestGapSentencesPassLintCausal(t *testing.T) {
	for _, g := range []string{GapNoTaskEvents, GapLocalExporterZone, GapTimeFormat} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}
