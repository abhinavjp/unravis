package iis

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
)

func TestHTTPERRStoresClientIP(t *testing.T) {
	evs, rep := mustParse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), httperrOpts())
	want := []string{"192.0.2.50", "192.0.2.51", "192.0.2.52", "192.0.2.53"}
	for i, ip := range want {
		got := evs[i].Attrs["client-ip"]
		if got != ip {
			t.Errorf("event %d client-ip = %q, want %q", i, got, ip)
		}
		if net.ParseIP(got) == nil {
			t.Errorf("event %d client-ip %q does not parse as an IP", i, got)
		}
	}
	out := dump(evs, rep)
	for _, banned := range []string{"192.0.2.10", "51000", "51001", "51002", "51003"} {
		if strings.Contains(out, banned) {
			t.Errorf("output contains %q", banned)
		}
	}
}

func TestHTTPERRNoClientIPColumnGap(t *testing.T) {
	var b strings.Builder
	b.WriteString("#Software: Microsoft HTTP API 2.0\n#Fields: date time cs-method cs-uri sc-status s-reason s-queuename\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "2001-03-17 10:00:%02d GET /a 503 QueueFull pool-alpha\n", i)
	}
	evs, rep := mustParse(t, NewHTTPERR(), []byte(b.String()), httperrOpts())
	if len(evs) != 40 || rep.Rejected != 0 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	for i, e := range evs {
		if _, ok := e.Attrs["client-ip"]; ok {
			t.Errorf("event %d has a client-ip attr, want none", i)
		}
	}
	found := false
	for _, g := range rep.KnownGaps {
		if g == gapHTTPERRNoClientIP {
			found = true
		}
	}
	if !found {
		t.Errorf("gaps %v, want %q", rep.KnownGaps, gapHTTPERRNoClientIP)
	}

	_, goodRep := mustParse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), httperrOpts())
	for _, g := range goodRep.KnownGaps {
		if g == gapHTTPERRNoClientIP {
			t.Errorf("fixture file declares c-ip, but gaps contain %q", gapHTTPERRNoClientIP)
		}
	}
}

func TestHTTPERRInvalidClientIP(t *testing.T) {
	long := strings.Repeat("9", 65)
	rows := []string{
		"2001-03-17 10:02:00 - GET /a 503 QueueFull pool-alpha",
		"2001-03-17 10:02:01 not-an-ip GET /a 503 QueueFull pool-alpha",
		"2001-03-17 10:02:02 1.2.3.4/32 GET /a 503 QueueFull pool-alpha",
		fmt.Sprintf("2001-03-17 10:02:03 %s GET /a 503 QueueFull pool-alpha", long),
	}
	evs, rep := mustParse(t, NewHTTPERR(), httperrFile(rows...), httperrOpts())
	if rep.Rejected != 0 {
		t.Fatalf("rejected %d, want 0", rep.Rejected)
	}
	for i := len(evs) - len(rows); i < len(evs); i++ {
		if _, ok := evs[i].Attrs["client-ip"]; ok {
			t.Errorf("event %d has a client-ip attr, want none", i)
		}
	}
}

func TestHTTPERRClientIPGapPassesCausalLint(t *testing.T) {
	if err := evidence.LintCausal(gapHTTPERRNoClientIP); err != nil {
		t.Errorf("LintCausal(%q) = %v, want nil", gapHTTPERRNoClientIP, err)
	}
}

func TestHTTPERRAllowedAttrsHasClientIP(t *testing.T) {
	allowed := NewHTTPERR().AllowedAttrs()
	if !allowed["client-ip"] {
		t.Error("AllowedAttrs()[\"client-ip\"] = false, want true")
	}
	if len(allowed) > 16 {
		t.Errorf("AllowedAttrs() has %d entries, want at most 16", len(allowed))
	}
}
