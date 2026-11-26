package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
	for i := 0; i < maxFactServices+20; i++ {
		in.Services = append(in.Services, ServiceFact{Name: fmt.Sprintf("svc%03d", i), State: "running", Start: "auto"})
	}
	in.Services = append(in.Services, ServiceFact{Name: "SVC000", State: "stopped", Start: "auto"}) // same service, other case
	for i := 0; i < maxFactCerts+20; i++ {
		in.Certs = append(in.Certs, CertFact{Subject: "CN=" + strings.Repeat("x", 300), Thumbprint: fmt.Sprintf("%040d", i), NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-i) * time.Hour).Format(time.RFC3339)})
	}
	in.Certs = append(in.Certs, in.Certs[0]) // the same certificate twice
	for i := 0; i < 40; i++ {
		in.Unread = append(in.Unread, "x")
	}
	out := boundFacts(in)
	if len(out.Services) != maxFactServices || len(out.Certs) != maxFactCerts || len(out.Unread) != 16 {
		t.Fatalf("sizes %d/%d/%d", len(out.Services), len(out.Certs), len(out.Unread))
	}
	if out.TimeZone != "GMT Standard Time" {
		t.Errorf("control character kept: %q", out.TimeZone)
	}
	if len(out.Certs[0].Subject) > maxFactText {
		t.Error("a long subject was not cut")
	}
	for i := 1; i < len(out.Certs); i++ {
		if out.Certs[i-1].NotAfter > out.Certs[i].NotAfter {
			t.Fatal("certificates are not sorted by end date")
		}
	}
	for i := 1; i < len(out.Services); i++ {
		if out.Services[i-1].Name >= out.Services[i].Name {
			t.Fatal("services are not sorted and unique")
		}
	}
	if len(in.Services) == len(out.Services) || len(in.Certs) == len(out.Certs) {
		t.Error("the input was not trimmed")
	}
	if _, err := json.Marshal(out); err != nil {
		t.Fatal(err)
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
