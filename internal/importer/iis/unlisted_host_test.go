package iis

// A real IIS log names its own server in s-computername. When that name is not
// in the host list, the computer the person picked for the file stands in for
// it; with no pick the file is refused. Synthetic names only.

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// w3cFrom is a W3C file whose rows all come from one server name.
func w3cFrom(name string, rows int) []byte {
	return []byte(strings.ReplaceAll(string(w3cFile(rows)), "WEB-A", name))
}

func TestW3CUnlistedNameUsesPickedHost(t *testing.T) {
	o := baseOpts()
	o.AllowUnlistedHost = true // the explicit import: without it the unlisted name is refused
	evs, rep := mustParse(t, NewW3C(), w3cFrom("ISS-PROD-07", 3), o)
	if len(evs) != 3 {
		t.Fatalf("events %d", len(evs))
	}
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Fatalf("host %q, want the picked label", e.Host)
		}
	}
	// the raw name is reported for the service, never stored on an event
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "ISS-PROD-07" {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
	// the same server in another letter case is still one machine
	data := []byte(strings.Replace(string(w3cFrom("ISS-PROD-07", 3)), "ISS-PROD-07 GET /api/v1/orders/1001", "iss-prod-07 GET /api/v1/orders/1001", 1))
	if evs, _ = mustParse(t, NewW3C(), data, o); len(evs) != 3 {
		t.Fatalf("case mix: events %d", len(evs))
	}
}

func TestW3CUnlistedNameNoPickedHostRefused(t *testing.T) {
	o := baseOpts()
	o.Host = nil
	_, rep, err := parse(t, NewW3C(), w3cFrom("ISS-PROD-07", 3), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v", err)
	}
	if len(rep.ObservedHosts) != 1 {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
	// a picked host with no label is no pick
	o.Host = &importer.HostHint{}
	if _, _, err = parse(t, NewW3C(), w3cFrom("ISS-PROD-07", 3), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("empty label: %v", err)
	}
}

func TestW3CListedNameIgnoresPickedHost(t *testing.T) {
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-b", Role: "iis"}
	evs, _ := mustParse(t, NewW3C(), w3cFile(3), o)
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Fatalf("host %q, want the listed label", e.Host)
		}
	}
	// no pick at all: the list alone decides, as before
	o.Host = nil
	if evs, _ = mustParse(t, NewW3C(), w3cFile(3), o); len(evs) != 3 || evs[0].Host != "web-a" {
		t.Fatalf("no pick: %+v", evs)
	}
}

func TestW3CUnlistedPlusListedKeepsMachinesApart(t *testing.T) {
	// a listed server and one unlisted server, the picked host is neither: both stand
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-b", Role: "iis"}
	o.AllowUnlistedHost = true // the unlisted ISS-PROD-07 falls back to the pick
	evs, _ := mustParse(t, NewW3C(), w3cFile(3, "2001-02-03 04:05:59 ISS-PROD-07 GET /a 200 5"), o)
	last := evs[len(evs)-1]
	if evs[0].Host != "web-a" || last.Host != "web-b" {
		t.Fatalf("hosts %q %q", evs[0].Host, last.Host)
	}
	// a second unlisted server would share the picked label: refused
	two := w3cFile(3, "2001-02-03 04:05:59 ISS-PROD-07 GET /a 200 5", "2001-02-03 04:05:59 ISS-PROD-08 GET /a 200 5")
	if _, _, err := parse(t, NewW3C(), two, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("two unlisted: %v", err)
	}
	// an unlisted server filed under the label a listed server already has: refused
	both := w3cFile(3, "2001-02-03 04:05:59 ISS-PROD-07 GET /a 200 5")
	if _, _, err := parse(t, NewW3C(), both, baseOpts()); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("shared label: %v", err)
	}
	// and in the other order
	rev := []byte("#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken\n" +
		"2001-02-03 04:05:01 ISS-PROD-07 GET /a 200 5\n2001-02-03 04:05:02 WEB-A GET /a 200 5\n")
	if _, _, err := parse(t, NewW3C(), rev, baseOpts()); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("shared label, listed last: %v", err)
	}
}

func TestW3CUnlistedPickedHostGivesNoTimeZone(t *testing.T) {
	// W3C times are UTC, so a picked host with no zone is fine and no zone is invented
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-b", Role: "iis"}
	o.AllowUnlistedHost = true
	evs, rep := mustParse(t, NewW3C(), w3cFrom("ISS-PROD-07", 2), o)
	if len(evs) != 2 || rep.Quality.TimezoneRule == "" {
		t.Fatalf("events %d rule %q", len(evs), rep.Quality.TimezoneRule)
	}
	if got := evs[0].Start.Location().String(); got != "UTC" {
		t.Fatalf("location %q", got)
	}
}

// wbHosts is a host list wired the way hosts.json is: labels looked up without
// regard to case, spaces or one trailing dot. It knows web-a and web-b only.
func wbHosts(observed string) (importer.HostHint, bool) {
	switch importer.HostKey(observed) {
	case "web-a":
		return importer.HostHint{Label: "web-a", Role: "iis"}, true
	case "web-b":
		return importer.HostHint{Label: "web-b", Role: "iis"}, true
	}
	return importer.HostHint{}, false
}

// A fully qualified spelling of a listed host resolves to that host, even when
// the person picked another computer for the file. The raw name reaches only
// the report.
func TestW3CFullyQualifiedListedNameResolvesToIt(t *testing.T) {
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-a", Role: "iis"}
	o.Hosts = wbHosts
	evs, rep := mustParse(t, NewW3C(), w3cFrom("WEB-B.corp.example.test", 3), o)
	if len(evs) != 3 {
		t.Fatalf("events %d", len(evs))
	}
	for _, e := range evs {
		if e.Host != "web-b" {
			t.Fatalf("host %q, want the listed label web-b", e.Host)
		}
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-B.corp.example.test" {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
}

// The machine names of one file are counted under their normalised key, so
// spellings that differ in case or carry a trailing dot are one machine: the
// file imports under the pick. A second distinct machine still refuses it.
func TestW3CSpellingsOfOneMachine(t *testing.T) {
	o := baseOpts()
	o.Host = &importer.HostHint{Label: "web-a", Role: "iis"}
	o.Hosts = wbHosts // no listed match for either spelling
	o.AllowUnlistedHost = true
	head := "#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken\n"
	file := head +
		"2001-02-03 04:05:11 WEB-X GET /a 200 5\n" +
		"2001-02-03 04:05:12 web-x. GET /a 200 5\n" +
		"2001-02-03 04:05:13 WEB-x. GET /a 200 5\n"
	evs, rep, err := parse(t, NewW3C(), []byte(file), o)
	if err != nil || len(evs) != 3 {
		t.Fatalf("events %d err %v", len(evs), err)
	}
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Fatalf("host %q, want the picked label", e.Host)
		}
	}
	if len(rep.ObservedHosts) != 3 {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
	// a different spelling of a different machine is not a spelling: refused
	two := head +
		"2001-02-03 04:05:11 WEB-X GET /a 200 5\n" +
		"2001-02-03 04:05:12 web-x. GET /a 200 5\n" +
		"2001-02-03 04:05:13 WEB-y. GET /a 200 5\n"
	if _, _, err = parse(t, NewW3C(), []byte(two), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("two machines: %v", err)
	}
}

// Without the explicit-import flag (host discovery), a wired host list that
// does not know the file's computer refuses the file even though a computer
// was picked for it.
func TestW3CUnlistedNameWithoutTheFlagRefused(t *testing.T) {
	o := baseOpts() // Host wired, Hosts wired, AllowUnlistedHost still false
	_, _, err := parse(t, NewW3C(), w3cFrom("ISS-PROD-07", 2), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}
