package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Synthetic canaries: none of these may reach a default events response.
const (
	canaryIP  = "CANARY_IP_7f2"
	canaryRef = "CANARY_REF_7f2"
	canaryCK  = "CANARY_CK_7f2"
)

// scrubCase makes a case holding one iis-w3c event with the three sensitive
// attributes plus status and method, stored as is.
func scrubCase(t *testing.T) (*intEnv, string) {
	t.Helper()
	e := newIntEnv(t, intRepo())
	id := e.newCase("Scrub canary case")
	ctx := context.Background()
	c, err := e.store.Open(ctx, casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Date(2001, 3, 17, 10, 0, 0, 0, time.UTC)
	ev := evidence.Event{
		EvidenceID: evidence.EventID(id, "iis-w3c", "scrub-nonce", 2, 0),
		Source:     "iis-w3c", Kind: "request", Host: "web-a",
		Start: start, End: start.Add(time.Second), Line: 2, TZRule: "utc-field",
		Attrs: map[string]string{
			"client-ip": canaryIP, "referer": canaryRef, "cookie:sid": canaryCK,
			"status": "200", "method": "GET",
		},
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "iis-w3c", KnownGaps: []string{"synthetic test data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		rep.Rows++
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
		return rep, nil
	}
	meta := casestore.ImportMeta{
		Importer: "iis-w3c", FileName: "scrub.log", HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import.",
	}
	if _, err := c.Import(ctx, meta, strings.NewReader("synthetic body\n"), run); err != nil {
		t.Fatal(err)
	}
	return e, id
}

func noCanary(t *testing.T, what, body string) {
	t.Helper()
	if strings.Contains(body, "CANARY_") {
		t.Fatalf("%s leaked a canary: %s", what, body)
	}
}

func TestCaseEventsDropSensitiveAttrsByDefault(t *testing.T) {
	e, id := scrubCase(t)
	res := e.get("/api/cases/" + id + "/events")
	if res.Code != http.StatusOK {
		t.Fatalf("events = %d %s", res.Code, res.Body)
	}
	noCanary(t, "default events", res.Body.String())
	events, _ := e.eventsOf(id, "")
	if len(events) != 1 || events[0].Attrs["status"] != "200" || events[0].Attrs["method"] != "GET" || len(events[0].Attrs) != 2 {
		t.Fatalf("attrs = %v", events)
	}
}

func TestCaseEventsIncludeSensitiveOnlyWithOne(t *testing.T) {
	e, id := scrubCase(t)
	events, _ := e.eventsOf(id, "?includeSensitive=1")
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	a := events[0].Attrs
	if a["client-ip"] != canaryIP || a["referer"] != canaryRef || a["cookie:sid"] != canaryCK || a["status"] != "200" {
		t.Fatalf("opt-in attrs = %v", a)
	}
	for _, v := range []string{"0", "true", "yes", "1x", "", "ONE", "1%20", "0&includeSensitive=1"} {
		res := e.get("/api/cases/" + id + "/events?includeSensitive=" + v)
		if res.Code != http.StatusOK {
			t.Fatalf("includeSensitive=%q = %d %s", v, res.Code, res.Body)
		}
		noCanary(t, "includeSensitive="+v, res.Body.String())
	}
	// A repeated parameter uses the first value only.
	res := e.get("/api/cases/" + id + "/events?includeSensitive=0&includeSensitive=1")
	noCanary(t, "repeated includeSensitive", res.Body.String())
}

func TestCaseEventsOptInStillNeedsTheCase(t *testing.T) {
	e, _ := scrubCase(t)
	unknown, err := casestore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{string(unknown), "not-a-case-id"} {
		plain := e.get("/api/cases/" + id + "/events")
		opted := e.get("/api/cases/" + id + "/events?includeSensitive=1")
		if plain.Code < 400 || opted.Code != plain.Code || opted.Body.String() != plain.Body.String() {
			t.Fatalf("id %q: plain %d %s vs opt-in %d %s", id, plain.Code, plain.Body, opted.Code, opted.Body)
		}
		if strings.Contains(opted.Body.String(), "events") || strings.Contains(opted.Body.String(), "attrs") {
			t.Fatalf("id %q: event data in %s", id, opted.Body)
		}
	}
	if res := e.get("/api/cases/" + string(unknown) + "/events?includeSensitive=1"); res.Code != http.StatusNotFound {
		t.Fatalf("unknown case = %d %s, want 404", res.Code, res.Body)
	}
}

func TestOutwardEventViewsUseTheProjection(t *testing.T) {
	raw, err := os.ReadFile("cases.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "ProjectEventAttrs") {
		t.Fatal("cases.go does not call evidence.ProjectEventAttrs")
	}
	if strings.Contains(src, "range e.Attrs") {
		t.Fatal("cases.go copies e.Attrs in a loop instead of using the projection")
	}
	// Later views (call trace, tracing detail) must be added to this list.
}
