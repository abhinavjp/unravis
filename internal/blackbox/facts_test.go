package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFactServiceWantedIsAFixedList(t *testing.T) {
	for _, n := range []string{"W3SVC", "WAS", "MSSQLSERVER", "MSSQL$REPORTING", "SQLSERVERAGENT", "SQLAgent$REPORTING", "Redis", "redis-6380", "salt-minion", "UnravisBlackbox", "UnravisBlackboxSql"} {
		if !factServiceWanted(n) {
			t.Errorf("%s should be reported", n)
		}
	}
	for _, n := range []string{"Spooler", "WinDefend", "MyAppService", "mssqlfdlauncher-not", ""} {
		if factServiceWanted(n) {
			t.Errorf("%s should not be reported", n)
		}
	}
}

func TestFactsAreBoundedSortedAndClean(t *testing.T) {
	in := &FactsGroup{OS: "Windows Server 2019 17763.1", TimeZone: "GMT Standard Time\x00", PendingReboot: true, SQL: map[string]string{"B": "16.0", "A": "15.0"}}
	// More than the caps, all distinct, so the cap (not the duplicate rule) is what trims them.
	for i := 0; i < maxFactServices+20; i++ {
		in.Services = append(in.Services, ServiceFact{Name: fmt.Sprintf("svc%03d", i), State: "running", Start: "auto"})
	}
	base := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxFactCerts+20; i++ {
		// Cert 0 ends first, cert N last; listed in REVERSE so a cut before the sort would keep the wrong ones.
		in.Certs = append([]CertFact{{Subject: "CN=" + strings.Repeat("x", 300), Thumbprint: fmt.Sprintf("%040d", i), NotAfter: base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)}}, in.Certs...)
	}
	for i := 0; i < 40; i++ {
		in.Unread = append(in.Unread, "x")
	}
	out := boundFacts(in)
	if len(out.Services) != maxFactServices || len(out.Certs) != maxFactCerts || len(out.Unread) != 16 {
		t.Fatalf("sizes %d/%d/%d", len(out.Services), len(out.Certs), len(out.Unread))
	}
	if out.Certs[0].Thumbprint != fmt.Sprintf("%040d", 0) || out.Certs[maxFactCerts-1].Thumbprint != fmt.Sprintf("%040d", maxFactCerts-1) {
		t.Errorf("the cut dropped the certificates that end soonest: first %s last %s", out.Certs[0].Thumbprint, out.Certs[maxFactCerts-1].Thumbprint)
	}
	if out.TimeZone != "GMT Standard Time" {
		t.Errorf("control character kept: %q", out.TimeZone)
	}
	if len(out.Certs[0].Subject) > maxFactText {
		t.Error("a long subject was not cut")
	}
	for i := 1; i < len(out.Services); i++ {
		if out.Services[i-1].Name >= out.Services[i].Name {
			t.Fatal("services are not sorted and unique")
		}
	}
	if _, err := json.Marshal(out); err != nil {
		t.Fatal(err)
	}
}

// A cut is always said out loud, so a missing certificate is never mistaken for an absent one.
func TestFactsSayWhenTheyAreCut(t *testing.T) {
	in := &FactsGroup{}
	for i := 0; i < maxFactCerts+1; i++ {
		in.Certs = append(in.Certs, CertFact{Thumbprint: fmt.Sprintf("%040d", i), NotAfter: "2027-01-01T00:00:00Z"})
	}
	if u := boundFacts(in).Unread; len(u) != 1 || u[0] != "certs: truncated" {
		t.Errorf("unread %v", u)
	}
}

func TestFactsDeduplicate(t *testing.T) {
	in := &FactsGroup{
		Services: []ServiceFact{{Name: "W3SVC", State: "running"}, {Name: "w3svc", State: "stopped"}, {Name: "WAS"}},
		Certs:    []CertFact{{Thumbprint: "AA", NotAfter: "2027-01-01T00:00:00Z"}, {Thumbprint: "AA", NotAfter: "2027-01-01T00:00:00Z"}, {Thumbprint: "BB", NotAfter: "2026-01-01T00:00:00Z"}},
	}
	out := boundFacts(in)
	if len(out.Services) != 2 || len(out.Certs) != 2 {
		t.Fatalf("services %d certs %d, want 2 and 2", len(out.Services), len(out.Certs))
	}
	if out.Certs[0].Thumbprint != "BB" {
		t.Error("the soonest end date should be first")
	}
}

func TestClipCutsOnACharacterBoundary(t *testing.T) {
	s := strings.Repeat("é", maxFactText) // two bytes each
	got := clip(s)
	if len(got) > maxFactText || !utf8.ValidString(got) {
		t.Errorf("clip gave %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestFactsGroupCollect(t *testing.T) {
	f := &factsGroup{read: func() *FactsGroup { return &FactsGroup{OS: "x", Unread: []string{"certs"}} }}
	v, err := f.Collect(time.Now(), 15)
	if err != nil {
		t.Fatal(err)
	}
	var s Sample
	applyGroup(&s, "facts", v)
	if s.Facts == nil || s.Facts.OS != "x" || len(s.Facts.Unread) != 1 {
		t.Errorf("%+v", s.Facts)
	}
	none := &factsGroup{read: func() *FactsGroup { return nil }}
	if _, err := none.Collect(time.Now(), 15); !errors.Is(err, errGroupUnavailable) {
		t.Errorf("a platform that answered nothing must be unavailable, got %v", err)
	}
}

func TestFactsGroupAbsentWhereThereIsNoPlatform(t *testing.T) {
	_, err := newFactsGroup(GroupDeps{})
	if factsSupported && err != nil {
		t.Errorf("supported platform: %v", err)
	}
	if !factsSupported && !errors.Is(err, errNotApplicable) {
		t.Errorf("unsupported platform: got %v, want errNotApplicable", err)
	}
}

func TestFactsAndPathsRunHourlyByDefault(t *testing.T) {
	c := testConfig(t)
	for _, id := range []string{"host.facts", "paths.files"} {
		if got := c.CollectorEvery(id); got != time.Hour {
			t.Errorf("%s runs every %v by default, want 1h", id, got)
		}
	}
}
