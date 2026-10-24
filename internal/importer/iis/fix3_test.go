package iis

// Tests for the batch 3 review fixes (H1, M1, M2, M3, M8, WAS, coverage).
// Synthetic values only: web-a, *.example.test, RFC 5737 addresses, 2001 dates.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func tzOpts(t *testing.T, zone string) importer.Options {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-a", Role: "iis", TZ: loc, Runtime: "iis-10"}
	return o
}

// ---------------------------------------------------------------- H1

// HTTPERR date and time are UTC (Microsoft "Error logging in HTTP APIs" and
// "Format of the HTTP Server API Error Logs"). The picked host's zone must not
// move any instant.
func TestHTTPERRTimestampsAreUTC(t *testing.T) {
	want := time.Date(2001, 3, 17, 10, 0, 1, 0, time.UTC)
	for _, o := range []importer.Options{
		tzOpts(t, "Pacific/Auckland"), tzOpts(t, "America/Los_Angeles"), tzOpts(t, "UTC"),
		func() importer.Options { o := baseOpts(); o.Host.TZ = nil; return o }(),
	} {
		evs, rep := mustParse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), o)
		if len(evs) != 4 {
			t.Fatalf("events %d", len(evs))
		}
		e := evs[0]
		if !e.Start.Equal(want) || !e.End.Equal(want) || e.TZRule != clock.RuleUTCField {
			t.Errorf("event 0 start %v end %v rule %q", e.Start, e.End, e.TZRule)
		}
		if rep.FieldRules["date+time"] != clock.RuleUTCField || rep.Quality.TimezoneRule != clock.RuleUTCField {
			t.Errorf("rules %v %+v", rep.FieldRules, rep.Quality)
		}
		for _, g := range rep.KnownGaps {
			if strings.Contains(g, "unverified") || strings.Contains(g, "host-local") {
				t.Errorf("stale gap %q", g)
			}
		}
	}
}

// UTC has no clock changes: rows whose wall time would be a fold or a gap in a
// host zone are ordinary instants.
func TestHTTPERRNoDSTRejects(t *testing.T) {
	evs, rep, err := parse(t, NewHTTPERR(), httperrFile(
		"2001-03-18 02:30:00 192.0.2.50 GET /a 503 QueueFull pool-alpha",
		"2001-10-07 02:30:00 192.0.2.50 GET /a 503 QueueFull pool-alpha"), tzOpts(t, "Pacific/Auckland"))
	if err != nil || rep.Rejected != 0 || len(evs) != 42 {
		t.Fatalf("err %v rejected %d events %d", err, rep.Rejected, len(evs))
	}
	if !evs[40].Start.Equal(time.Date(2001, 3, 18, 2, 30, 0, 0, time.UTC)) {
		t.Errorf("fold row %v", evs[40].Start)
	}
}

// No zone is needed, but the host label still is.
func TestHTTPERRNeedsOnlyHostLabel(t *testing.T) {
	o := baseOpts()
	o.Host.TZ = nil
	if _, _, err := parse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), o); err != nil {
		t.Fatalf("no tz: %v", err)
	}
	o.Host = nil
	if _, _, err := parse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("no host: %v", err)
	}
	o = baseOpts()
	o.Host.Label = ""
	if _, _, err := parse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("empty label: %v", err)
	}
}

// ---------------------------------------------------------------- M1

func TestW3CManyMachinesWithoutHostMapRefused(t *testing.T) {
	data := w3cFile(3, "2001-02-03 04:05:59 DB-B GET /a 200 5")
	o := baseOpts()
	o.Hosts = nil
	_, rep, err := parse(t, NewW3C(), data, o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v", err)
	}
	seen := strings.Join(rep.ObservedHosts, ",")
	if !strings.Contains(seen, "WEB-A") || !strings.Contains(seen, "DB-B") {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
	// the same machine spelled in another case is still one machine
	data = []byte(strings.Replace(string(w3cFile(3)), "WEB-A GET /api/v1/orders/1001", "web-a GET /api/v1/orders/1001", 1))
	evs, _ := mustParse(t, NewW3C(), data, o)
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Fatalf("host %q", e.Host)
		}
	}
	// with a host map, each machine maps on its own and an unknown one is refused
	_, _, err = parse(t, NewW3C(), data, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
}

func TestW3COverlongMachineNameDoesNotFallBack(t *testing.T) {
	long := strings.Repeat("x", maxHostNameBytes+1)
	data := w3cFile(40, "2001-02-03 04:05:59 "+long+" GET /a 200 5")
	o := baseOpts()
	o.Hosts = nil
	evs, rep, err := parse(t, NewW3C(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonUnknownHost {
		t.Fatalf("events %d rejected %d errors %+v", len(evs), rep.Rejected, rep.Errors)
	}
}

// ---------------------------------------------------------------- M2

func TestFixedGapsPassCausalLint(t *testing.T) {
	gaps := []string{gapW3CTimeEnd, gapW3CTimeStart, gapTimeTaken, gapNoTimeTaken, gapW3CResolution, recycleGapText, recycleGapTextLegacy}
	for _, rt := range []string{"", "unknown", "iis-7.5", "iis-8", "iis-8.0", "iis-8.5", "iis-9", "iis-10", "iis-10.0", "IIS-11"} {
		gaps = append(gaps, RecycleGap(rt))
	}
	for _, f := range [][]byte{fixture(t, "w3c_good.log"), w3cFile(3, "x")} {
		_, rep, _ := parse(t, NewW3C(), f, baseOpts())
		gaps = append(gaps, rep.KnownGaps...)
	}
	_, rep, _ := parse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), baseOpts())
	gaps = append(gaps, rep.KnownGaps...)
	for _, g := range gaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestRecycleGapWordingHasNoCauseWords(t *testing.T) {
	for _, rt := range []string{"iis-8.0", "iis-10"} {
		g := RecycleGap(rt)
		if !strings.Contains(g, "triggers may have left no event") {
			t.Errorf("RecycleGap(%q) = %q", rt, g)
		}
	}
}

// ---------------------------------------------------------------- M3

func TestStemScrubsIdentifiers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/users/alice@example.test/profile", "/users/{id}/profile"},
		{"/orders/507f1f77bcf86cd799439011", "/orders/{id}"},
		{"/orders/507F1F77BCF86CD799439011/lines", "/orders/{id}/lines"},
		{"/cart;jsessionid=ABCDEF0123456789/view", "/cart/view"},
		{"/cart/view;jsessionid=ABCDEF0123456789", "/cart/view"},
		{"/reset/eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl", "/reset/{id}"},
		{"/tokens/a1B2c3D4e5F6g7H8i9J0k1L2", "/tokens/{id}"},
		{"/tokens/0123456789abcdef", "/tokens/{id}"},
		{"/files/john.smith", "/files/{id}"},
		{"/a/key=value/b", "/a/{id}/b"},
		{"/a/John%20Smith/b", "/a/{id}/b"},
		{"/a/ORD-2001-00042/b", "/a/{id}/b"},
		{"/a/" + strings.Repeat("Z", 40), "/a/{id}"},
		// route words stay
		{"/api/v1/orders", "/api/v1/orders"},
		{"/api/v1.0/orders", "/api/v1.0/orders"},
		{"/Home/Index", "/Home/Index"},
		{"/Default.aspx", "/Default.aspx"},
		{"/static/app.js", "/static/app.js"},
		{"/line_items/by-status", "/line_items/by-status"},
		{"/api/orders/v1x", "/api/orders/v1x"},
	}
	for _, c := range cases {
		got, ok := normaliseStem(c.in)
		if !ok || got != c.want {
			t.Errorf("normaliseStem(%q) = %q,%v want %q", c.in, got, ok, c.want)
		}
	}
}

// Whatever the cell holds, none of these shapes survives into an attribute.
func TestStemIdentifiersNeverReachEvents(t *testing.T) {
	banned := []string{"alice@", "507f1f77", "jsessionid", "ABCDEF0123", "eyJ", "c2lnbmF0", "a1B2c3D4"}
	rows := []string{
		"/users/alice@example.test/profile", "/orders/507f1f77bcf86cd799439011",
		"/cart;jsessionid=ABCDEF0123456789/view", "/reset/eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl",
		"/tokens/a1B2c3D4e5F6g7H8i9J0k1L2",
	}
	var extra []string
	for _, r := range rows {
		extra = append(extra, "2001-02-03 04:05:59 WEB-A GET "+r+" 200 5")
	}
	evs, _ := mustParse(t, NewW3C(), w3cFile(40, extra...), baseOpts())
	for _, e := range evs {
		for _, b := range banned {
			if strings.Contains(e.Attrs["stem"], b) {
				t.Errorf("stem %q keeps %q", e.Attrs["stem"], b)
			}
		}
	}
	var hx []string
	for _, r := range rows {
		hx = append(hx, "2001-03-17 10:01:01 192.0.2.50 GET "+r+" 503 QueueFull pool-alpha")
	}
	evs, _ = mustParse(t, NewHTTPERR(), httperrFile(hx...), baseOpts())
	for _, e := range evs {
		for _, b := range banned {
			if strings.Contains(e.Attrs["stem"], b) {
				t.Errorf("httperr stem %q keeps %q", e.Attrs["stem"], b)
			}
		}
	}
}

// ---------------------------------------------------------------- M8

func TestW3COutOfRangeYearsRejectOnlyTheRow(t *testing.T) {
	for _, bad := range []string{
		"0001-01-01 00:00:00", "1601-01-01 00:00:00", "1989-12-31 23:59:59", "2101-01-01 00:00:00", "9999-12-31 23:59:59",
	} {
		evs, rep, err := parse(t, NewW3C(), w3cFile(50, bad+" WEB-A GET /a 200 5"), baseOpts())
		if err != nil {
			t.Fatalf("%s: %v", bad, err)
		}
		if len(evs) != 50 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime {
			t.Fatalf("%s: events %d rejected %d errors %+v", bad, len(evs), rep.Rejected, rep.Errors)
		}
	}
	// the bounds themselves are fine
	evs, _ := mustParse(t, NewW3C(), w3cFile(50,
		"1990-01-01 00:00:00 WEB-A GET /a 200 5", "2100-12-31 23:59:59 WEB-A GET /a 200 5"), baseOpts())
	if len(evs) != 52 {
		t.Fatalf("events %d", len(evs))
	}
}

func TestHTTPERROutOfRangeYearsRejectOnlyTheRow(t *testing.T) {
	for _, bad := range []string{"0001-01-01 00:00:00", "1601-01-01 00:00:00", "2101-01-01 00:00:00"} {
		evs, rep, err := parse(t, NewHTTPERR(), httperrFile(bad+" 192.0.2.50 GET /a 503 QueueFull pool-alpha"), baseOpts())
		if err != nil {
			t.Fatalf("%s: %v", bad, err)
		}
		if len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime {
			t.Fatalf("%s: events %d rejected %d errors %+v", bad, len(evs), rep.Rejected, rep.Errors)
		}
	}
}

// ---------------------------------------------------------------- WAS

func TestClassifyWAS5078(t *testing.T) {
	kind, attrs, ok := ClassifyWAS("Microsoft-Windows-WAS", 5078)
	if !ok || kind != "apppool-recycle" || attrs["reason"] != "isapi-unhealthy" || len(attrs) != 1 {
		t.Fatalf("5078: %q %v %v", kind, attrs, ok)
	}
}

func TestRecycleGapCoversEveryVersion(t *testing.T) {
	// every version, IIS 10 included, only logs the triggers in logEventOnRecycle
	for _, rt := range []string{"iis-10", "iis-10.0", "IIS-11", " iis-10 ", "iis-8.5", "iis-9"} {
		g := RecycleGap(rt)
		if g == "" || !strings.Contains(g, "logEventOnRecycle") || strings.Contains(g, "Time, Memory and PrivateMemory") {
			t.Errorf("RecycleGap(%q) = %q", rt, g)
		}
	}
	// before 8.5 (or unknown) the default set is the narrow one
	for _, rt := range []string{"iis-8", "iis-8.0", "iis-7.5", "", "unknown", "sql-2019", "iis-", "iis-x"} {
		g := RecycleGap(rt)
		if g == "" || !strings.Contains(g, "Time, Memory and PrivateMemory") || !strings.Contains(g, "logEventOnRecycle") {
			t.Errorf("RecycleGap(%q) = %q", rt, g)
		}
	}
}

// ---------------------------------------------------------------- coverage

func TestW3CCoverageIncludesRequestStart(t *testing.T) {
	data := []byte("#Fields: date time s-computername cs-method time-taken\n" +
		"2001-02-03 04:05:06 WEB-A GET 305000\n")
	_, rep := mustParse(t, NewW3C(), data, baseOpts())
	wantFirst := time.Date(2001, 2, 3, 4, 0, 1, 0, time.UTC)
	wantLast := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if !rep.First.Equal(wantFirst) || !rep.Last.Equal(wantLast) {
		t.Fatalf("coverage %v %v", rep.First, rep.Last)
	}
	if rep.Quality.CoverageFirst == nil || !rep.Quality.CoverageFirst.Equal(wantFirst) {
		t.Fatalf("quality %+v", rep.Quality)
	}
	if !strings.Contains(strings.Join(rep.KnownGaps, "\n"), "one-second resolution") {
		t.Fatalf("gaps %v", rep.KnownGaps)
	}
}
