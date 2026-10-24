package evidence

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
)

// All values below are synthetic: 2001 dates, RFC 5737 addresses, invented
// names. The sentinel stands for any client value that must never reach a
// case export.
const caseSentinel = "SENTINEL-7f3a"

var caseT0 = time.Date(2001, 3, 4, 10, 0, 0, 0, time.UTC)

func caseKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func caseEvID(source string, n int) string { return fmt.Sprintf("ev:case0001:%s:%016x", source, n) }

func caseEvent(source, kind string, n int, attrs map[string]string) Event {
	at := caseT0.Add(time.Duration(n) * time.Second)
	d := 12.5
	return Event{EvidenceID: caseEvID(source, n), Source: source, Kind: kind, Host: "web-a", Start: at, End: at,
		DurationMs: &d, Unit: "ms", Severity: "info", Line: int64(n + 2), TZRule: clock.RuleUTCField, Attrs: attrs}
}

// storedUser is a user value as the case store holds it: already a case-keyed
// pseudonym. It must never reach the export.
func storedUser(u int) string { return fmt.Sprintf("user-%08x", 0xa0000000+u) }

// userEvents returns IIS events: users distinct users with per events each,
// then none events whose user is noneVal (no attribute when noneVal is "").
func userEvents(users, per, none int, noneVal string, first int) []Event {
	var out []Event
	n := first
	for u := 0; u < users; u++ {
		for i := 0; i < per; i++ {
			out = append(out, caseEvent("iis-w3c", "request", n, map[string]string{"method": "GET", "status": "200", "user": storedUser(u)}))
			n++
		}
	}
	for i := 0; i < none; i++ {
		attrs := map[string]string{"method": "GET", "status": "200"}
		if noneVal != "" {
			attrs["user"] = noneVal
		}
		out = append(out, caseEvent("iis-w3c", "request", n, attrs))
		n++
	}
	return out
}

func caseImports() []CaseImport {
	first, last := caseT0, caseT0.Add(time.Hour)
	return []CaseImport{
		{Importer: "iis-w3c", FileName: "u_ex010304-" + caseSentinel + ".log", SHA256: sha256Hex("file-a"),
			HostLabel: "web-a-" + caseSentinel, TZRule: clock.RuleUTCField, Status: "ok", Rows: 5000, Rejected: 40,
			Quality:   SourceQuality{SourceVersion: "W3C 1.0", Sampling: "none", TimezoneRule: "utc-field", CoverageFirst: &first, CoverageLast: &last},
			KnownGaps: []string{"Only finished requests are logged; 3 rows had no time."},
			AckText:   "I confirm this file holds no passwords."},
		{Importer: "schedule-hangfire", FileName: "jobs-" + caseSentinel + ".csv", SHA256: sha256Hex("file-b"),
			HostLabel: "app-a", TZRule: clock.RuleHostLocal, Rows: 30, Rejected: 2},
		{Importer: "custom-thing", FileName: caseSentinel + ".txt", SHA256: sha256Hex("file-c"), Rows: 100, Rejected: 0},
		{Importer: "iis-w3c", FileName: "broken-" + caseSentinel + ".log", SHA256: "not a hash", Status: "failed"},
	}
}

// caseEvents plants the sentinel everywhere it must be removed or re-keyed.
func caseEvents() []Event {
	ev := userEvents(20, 20, 0, "", 0)
	ev[0].Host = "web-a-" + caseSentinel
	ev[1].Host = "ＳＥＮＴＩＮＥＬ-7f3a" // full-width look-alike of the sentinel
	ev[2].Attrs["stem"] = "/api/orders/" + caseSentinel
	ev[3].Attrs["site"] = "Default Web Site " + caseSentinel
	ev[4].Attrs["cookie"] = caseSentinel // not on any allowlist
	ev = append(ev,
		caseEvent("iis-httperr", "httperr", 1000, map[string]string{"reason": "Timer_ConnectionIdle", "status": "503", "queue": "AppPool-" + caseSentinel}),
		caseEvent("win-events", "apppool-recycle", 1001, map[string]string{"log": "System", "id": "5074", "app-pool": "AppPool-" + caseSentinel}),
		caseEvent("win-counters", "counter", 1002, map[string]string{"object": "Process", "counter": "% Processor Time", "instance": "_Total"}),
		caseEvent("win-counters", "counter", 1003, map[string]string{"object": "Process", "counter": "% Processor Time", "instance": "w3wp-" + caseSentinel}),
		caseEvent("mongod-json", "slow-op", 1004, map[string]string{"ns": "orders." + caseSentinel, "command": "find", "planSummary": "COLLSCAN", "docsExamined": "12000", "queryHash": "A1B2C3D4"}),
		caseEvent("appinsights", "dependency", 1005, map[string]string{"dependencyType": "SQL", "resultCode": "0", "name": "GET /api/" + caseSentinel, "operation_Id": caseSentinel}),
		caseEvent("schedule-hangfire", "job-run", 1006, map[string]string{"job": "job-alpha-" + caseSentinel, "queue": "default", "freq": "hourly"}),
		caseEvent("custom-thing", "other", 1007, map[string]string{"x": caseSentinel}),
	)
	return ev
}

func caseInput(key []byte) CaseInput {
	return CaseInput{Imports: caseImports(), Events: caseEvents(), ToolVersion: "0.13.0-test", Now: testNow, Key: key}
}

func mustBuildCase(t *testing.T, in CaseInput) (Export, []byte) {
	t.Helper()
	e, err := BuildCase(in)
	if err != nil {
		t.Fatalf("BuildCase: %v", err)
	}
	raw, err := Canonical(e)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	return e, raw
}

func mustCSV(t *testing.T, e Export) []byte {
	t.Helper()
	raw, err := EventsCSV(e)
	if err != nil {
		t.Fatalf("EventsCSV: %v", err)
	}
	return raw
}

func eventByID(t *testing.T, e Export, id string) ExportEvent {
	t.Helper()
	for _, ev := range e.Body.Events {
		if ev.EvidenceID == id {
			return ev
		}
	}
	t.Fatalf("event %s not in export", id)
	return ExportEvent{}
}

func hasStatementPart(e Export, part string) bool {
	for _, s := range e.Manifest.Masking.Statements {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

var pseudonymPattern = regexp.MustCompile(`\b(?:host|user|site|pool|ns|job|queue|instance)-[0-9a-f]{8}\b`)

func TestBuildCaseShape(t *testing.T) {
	e, raw := mustBuildCase(t, caseInput(caseKey(1)))
	if err := Verify(raw); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if e.Manifest.SchemaVersion != 2 || e.Body.Records == nil || len(e.Body.Records) != 0 {
		t.Fatalf("want version 2 with an empty records list, got %d / %v", e.Manifest.SchemaVersion, e.Body.Records)
	}
	// The failed import is skipped; the unknown importer is "other".
	if len(e.Manifest.Imports) != 3 || len(e.Body.Imports) != 3 {
		t.Fatalf("imports: manifest %d, body %d, want 3", len(e.Manifest.Imports), len(e.Body.Imports))
	}
	for i, want := range []string{"iis-w3c", "schedule-hangfire", otherImporter} {
		if m, b := e.Manifest.Imports[i], e.Body.Imports[i]; m.Importer != want || b.Importer != want || m.Ref != b.Ref || m.Ref != fmt.Sprintf("import-%d", i+1) {
			t.Errorf("import %d: %+v / %+v, want importer %s", i, m, b, want)
		}
	}
	m := e.Manifest.Imports[0]
	if m.SHA256 != sha256Hex("file-a") || m.Acknowledgement != "I confirm this file holds no passwords." {
		t.Errorf("manifest import: %+v", m)
	}
	if len(m.KnownGaps) != 1 || !strings.Contains(m.KnownGaps[0], "<20 rows") {
		t.Errorf("gap counts must be masked: %q", m.KnownGaps)
	}
	if e.Manifest.Imports[1].KnownGaps == nil {
		t.Error("knownGaps must be an empty list, not null")
	}
	b := e.Body.Imports[0]
	if b.Rows.Text != "5000" || b.Rejected.Text != "40" || b.TZRule != clock.RuleUTCField || !strings.HasPrefix(b.Host, "host-") {
		t.Errorf("body import: %+v", b)
	}
	if b.Quality.SourceVersion != "W3C 1.0" || b.Quality.CoverageFirst == nil {
		t.Errorf("quality: %+v", b.Quality)
	}
	// 20 IIS users of 20 events each, plus 7 listed events; the unknown source
	// is left out.
	if len(e.Body.Events) != 407 {
		t.Errorf("events: %d, want 407", len(e.Body.Events))
	}
	for i := 1; i < len(e.Body.Events); i++ {
		if e.Body.Events[i].Start.Before(e.Body.Events[i-1].Start) {
			t.Fatal("events not sorted by start")
		}
	}
	for _, want := range []string{"Some events came from a source without an export policy", "Each user is shown as a keyed pseudonym", "withheld\" is 20 or more"} {
		if !hasStatementPart(e, want) {
			t.Errorf("missing statement %q", want)
		}
	}
	for _, s := range e.Manifest.Masking.Statements {
		if err := LintCausal(s); err != nil || containsProven(s) || containsStrong(s) {
			t.Errorf("statement fails the claim checks: %q", s)
		}
	}
}

func TestBuildCaseAttributePolicy(t *testing.T) {
	e, _ := mustBuildCase(t, caseInput(caseKey(1)))
	iis := eventByID(t, e, caseEvID("iis-w3c", 2))
	if _, ok := iis.Attrs["stem"]; ok {
		t.Error("the URL stem must be left out")
	}
	if iis.Attrs["method"] != "GET" || iis.Attrs["status"] != "200" {
		t.Errorf("kept attributes: %v", iis.Attrs)
	}
	if _, ok := eventByID(t, e, caseEvID("iis-w3c", 4)).Attrs["cookie"]; ok {
		t.Error("an unlisted attribute must be left out")
	}
	if s := eventByID(t, e, caseEvID("iis-w3c", 3)).Attrs["site"]; !strings.HasPrefix(s, "site-") {
		t.Errorf("site must be a pseudonym, got %q", s)
	}
	httperr := eventByID(t, e, caseEvID("iis-httperr", 1000))
	recycle := eventByID(t, e, caseEvID("win-events", 1001))
	if httperr.Attrs["queue"] != recycle.Attrs["app-pool"] || !strings.HasPrefix(httperr.Attrs["queue"], "pool-") {
		t.Errorf("one pool must read the same in HTTPERR and the event log: %q / %q", httperr.Attrs["queue"], recycle.Attrs["app-pool"])
	}
	if got := eventByID(t, e, caseEvID("win-counters", 1002)).Attrs["instance"]; got != "_Total" {
		t.Errorf("_Total is kept as is, got %q", got)
	}
	if got := eventByID(t, e, caseEvID("win-counters", 1003)).Attrs["instance"]; !strings.HasPrefix(got, "instance-") {
		t.Errorf("a counter instance is a pseudonym, got %q", got)
	}
	mongo := eventByID(t, e, caseEvID("mongod-json", 1004))
	if !strings.HasPrefix(mongo.Attrs["ns"], "ns-") || mongo.Attrs["planSummary"] != "COLLSCAN" || mongo.Attrs["docsExamined"] != "12000" || mongo.Attrs["queryHash"] != "A1B2C3D4" {
		t.Errorf("mongod attributes: %v", mongo.Attrs)
	}
	ai := eventByID(t, e, caseEvID("appinsights", 1005))
	if _, ok := ai.Attrs["name"]; ok {
		t.Error("the operation name must be left out")
	}
	if _, ok := ai.Attrs["operation_Id"]; ok {
		t.Error("the operation id must be left out")
	}
	if got := eventByID(t, e, caseEvID("schedule-hangfire", 1006)).Attrs["job"]; !strings.HasPrefix(got, "job-") || strings.Contains(got, "alpha") {
		t.Errorf("job must be a pseudonym, got %q", got)
	}
}

// Re-identification: no pseudonym is shared between two exports of the same
// case under different keys, and no stored or raw value reaches either file.
func TestBuildCaseReidentification(t *testing.T) {
	ea, rawA := mustBuildCase(t, caseInput(caseKey(1)))
	eb, rawB := mustBuildCase(t, caseInput(caseKey(2)))
	csvA, csvB := mustCSV(t, ea), mustCSV(t, eb)

	a := map[string]bool{}
	prefixes := map[string]bool{}
	for _, p := range pseudonymPattern.FindAllString(string(rawA), -1) {
		a[p] = true
		prefixes[p[:strings.IndexByte(p, '-')]] = true
	}
	for _, want := range []string{"host", "user", "site", "pool", "ns", "job", "queue", "instance"} {
		if !prefixes[want] {
			t.Errorf("fixture should produce %s pseudonyms", want)
		}
	}
	for _, p := range pseudonymPattern.FindAllString(string(rawB), -1) {
		if a[p] {
			t.Errorf("pseudonym %s appears under both keys", p)
		}
	}
	for _, out := range [][]byte{rawA, rawB, csvA, csvB, []byte(normalizeText(string(rawA))), []byte(normalizeText(string(csvA)))} {
		assertAbsent(t, out, caseSentinel, "web-a", "app-a", "u_ex010304", "job-alpha", "orders.", "/api/")
		for u := 0; u < 20; u++ {
			assertAbsent(t, out, storedUser(u))
		}
	}
}

func TestBuildCaseRawUserNamesNeverAppear(t *testing.T) {
	names := []string{"alice.example", "CORP-bob", "carol"}
	for _, users := range []int{len(names), ThinCell} { // withheld, then shown
		var ev []Event
		n := 0
		for u := 0; u < users; u++ {
			name := fmt.Sprintf("member-%02d", u)
			if u < len(names) {
				name = names[u]
			}
			for i := 0; i < ThinCell; i++ {
				ev = append(ev, caseEvent("iis-w3c", "request", n, map[string]string{"user": name}))
				n++
			}
		}
		e, raw := mustBuildCase(t, CaseInput{Events: ev, Now: testNow, Key: caseKey(3)})
		assertAbsent(t, raw, names...)
		assertAbsent(t, mustCSV(t, e), names...)
		assertAbsent(t, raw, "member-")
	}
}

var ipv4Out = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// R8-B3: no IP address and no raw user id in any bundle.
func TestBuildCaseNoIPsOrUserIDs(t *testing.T) {
	ev := []Event{
		caseEvent("iis-w3c", "request", 0, map[string]string{"method": "192.0.2.10", "status": "198.51.100.7", "port": "443"}),
		caseEvent("iis-httperr", "httperr", 1, map[string]string{"reason": "from 203.0.113.9", "status": "400"}),
		caseEvent("appinsights", "dependency", 2, map[string]string{"resultCode": "2001:db8::1", "dependencyType": "user 4242"}),
		caseEvent("win-events", "event", 3, map[string]string{"reason": "web-a.example.test", "provider": "uid=98765", "id": "192.0.2.11"}),
		caseEvent("sql-xe", "wait", 4, map[string]string{"wait_type": "LCK_M_X", "state": "12345678"}),
	}
	imports := []CaseImport{{Importer: "iis-w3c", SHA256: sha256Hex("f"), HostLabel: "192.0.2.12", Rows: 50, Rejected: 30,
		AckText:   "Exported from 198.51.100.20 by user 4242.",
		KnownGaps: []string{"Client IP 203.0.113.30 is not kept.", "uid=98765 rows skipped"}}}
	e, raw := mustBuildCase(t, CaseInput{Imports: imports, Events: ev, Now: testNow, Key: caseKey(4)})
	csvRaw := mustCSV(t, e)
	for _, out := range [][]byte{raw, csvRaw} {
		if m := ipv4Out.Find(out); m != nil {
			t.Errorf("an IPv4 address reached the bundle: %s", m)
		}
		assertAbsent(t, out, "2001:db8", "user 4242", "98765", "example.test", "12345678")
	}
	want := map[string]map[string]string{
		caseEvID("iis-w3c", 0):     {"method": removedText, "status": removedText, "port": "443"},
		caseEvID("iis-httperr", 1): {"reason": removedText, "status": "400"},
		caseEvID("appinsights", 2): {"resultCode": removedText, "dependencyType": removedText},
		caseEvID("win-events", 3):  {"reason": removedText, "provider": removedText, "id": removedText},
		caseEvID("sql-xe", 4):      {"wait_type": "LCK_M_X", "state": removedText},
	}
	for id, attrs := range want {
		got := eventByID(t, e, id).Attrs
		for k, v := range attrs {
			if got[k] != v {
				t.Errorf("%s %s = %q, want %q", id, k, got[k], v)
			}
		}
	}
}

// R8-B7: event-derived text never says "proven", "strong correlation" or
// names a cause.
func TestBuildCaseNoClaims(t *testing.T) {
	ev := []Event{
		caseEvent("iis-httperr", "httperr", 0, map[string]string{"reason": "proven cause"}),
		caseEvent("iis-httperr", "httperr", 1, map[string]string{"reason": "Strong Correlation"}),
		caseEvent("iis-httperr", "httperr", 2, map[string]string{"reason": "PRОVEN"}), // Cyrillic O
		caseEvent("iis-httperr", "httperr", 3, map[string]string{"reason": "due to lock"}),
		caseEvent("win-events", "event", 4, map[string]string{"reason": "Strong_Correlat"}),
		caseEvent("sql-xe", "caused-by", 5, map[string]string{"wait_type": "blocked by sql-a"}),
		caseEvent("sql-xe", "strong-correlation", 6, nil),
	}
	imports := []CaseImport{{Importer: "iis-httperr", SHA256: sha256Hex("f"), Rows: 50, Rejected: 30,
		AckText:   "This import is proven safe.",
		KnownGaps: []string{"A strong correlation with load is shown.", "Rows drop because of rotation.", "Gaps are unprоven."},
		Quality:   SourceQuality{Sampling: "caused by load", Threshold: "proven", SourceVersion: "HTTPERR 1.0"}}}
	e, raw := mustBuildCase(t, CaseInput{Imports: imports, Events: ev, Now: testNow, Key: caseKey(5)})
	for _, out := range [][]byte{raw, mustCSV(t, e)} {
		if containsProven(string(out)) || containsStrong(string(out)) || LintCausal(string(out)) != nil {
			t.Errorf("bundle carries a claim:\n%s", out)
		}
	}
	for _, x := range e.Body.Events {
		for k, v := range x.Attrs {
			if v != removedText {
				t.Errorf("%s %s = %q, want %q", x.EvidenceID, k, v, removedText)
			}
		}
		if strings.HasPrefix(x.Source, "sql-xe") && x.Kind != otherKind {
			t.Errorf("kind %q must become %q", x.Kind, otherKind)
		}
	}
	m := e.Manifest.Imports[0]
	if m.Acknowledgement != ackWithheld {
		t.Errorf("acknowledgement %q, want withheld", m.Acknowledgement)
	}
	for _, g := range m.KnownGaps {
		if g != gapWithheld {
			t.Errorf("gap %q, want withheld", g)
		}
	}
	if q := e.Body.Imports[0].Quality; q.Sampling != "" || q.Threshold != "" || q.SourceVersion != "HTTPERR 1.0" {
		t.Errorf("quality: %+v", q)
	}
}

func countUserAttrs(e Export) (n int) {
	for _, ev := range e.Body.Events {
		if _, ok := ev.Attrs["user"]; ok {
			n++
		}
	}
	return n
}

// Thin cells: a user pseudonym is a cell (its events can be counted), so the
// attribute is all or nothing.
func TestBuildCaseThinUsers(t *testing.T) {
	oneShort := userEvents(ThinCell, ThinCell, 0, "", 0)
	oneShort = oneShort[1:] // the first user now has 19 events
	for _, tc := range []struct {
		name   string
		events []Event
		shown  int
	}{
		{"19 users", userEvents(ThinCell-1, ThinCell, 0, "", 0), 0},
		{"one user with 19 events", oneShort, 0},
		{"5 events without a user", userEvents(ThinCell, ThinCell, 5, "", 0), 0},
		{"5 events with a dash", userEvents(ThinCell, ThinCell, 5, "-", 0), 0},
		{"single user single row", userEvents(1, 1, 0, "", 0), 0},
		{"20 by 20", userEvents(ThinCell, ThinCell, 0, "", 0), ThinCell * ThinCell},
		{"20 by 20 plus 20 without", userEvents(ThinCell, ThinCell, ThinCell, "-", 0), ThinCell * ThinCell},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, raw := mustBuildCase(t, CaseInput{Events: tc.events, Now: testNow, Key: caseKey(6)})
			if got := countUserAttrs(e); got != tc.shown {
				t.Errorf("events with a user: %d, want %d", got, tc.shown)
			}
			if bytes.Contains(raw, []byte(`"user": "-"`)) {
				t.Error("a dash user must not be exported")
			}
			wantStmt := "Each user is shown as a keyed pseudonym"
			if tc.shown == 0 {
				wantStmt = "The user attribute is left out of every event"
			}
			if !hasStatementPart(e, wantStmt) {
				t.Errorf("missing statement %q", wantStmt)
			}
			assertAbsent(t, raw, "user-a0000")
		})
	}
	// No IIS events at all: nothing to say about users beyond "none".
	e, _ := mustBuildCase(t, CaseInput{Events: []Event{caseEvent("sql-errorlog", "error", 0, nil)}, Now: testNow, Key: caseKey(6)})
	if !hasStatementPart(e, "No user pseudonyms are included.") {
		t.Error("missing the no-pseudonyms statement")
	}
}

func TestBuildCaseImportCounts(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		rows, rejected         int64
		wantRows, wantRejected string
		wantWithheldStatement  bool
	}{
		{"single row", 1, 0, "<20", "<20", false},
		{"rejected below 20", 500, 3, withheldText, "<20", true},
		{"both shown", 500, 25, "500", "25", false},
		{"both thin", 10, 2, "<20", "<20", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := CaseInput{Imports: []CaseImport{{Importer: "sql-errorlog", SHA256: sha256Hex("f"), Rows: tc.rows, Rejected: tc.rejected}},
				Events: []Event{caseEvent("sql-errorlog", "error", 0, map[string]string{"error_number": "18456"})}, Now: testNow, Key: caseKey(7)}
			e, raw := mustBuildCase(t, in)
			b := e.Body.Imports[0]
			if b.Rows.Text != tc.wantRows || b.Rejected.Text != tc.wantRejected {
				t.Errorf("rows %q rejected %q, want %q %q", b.Rows.Text, b.Rejected.Text, tc.wantRows, tc.wantRejected)
			}
			if (b.Rows.Value == nil) != b.Rows.Masked {
				t.Error("a masked count must carry no value")
			}
			if hasStatementPart(e, "is hidden so that subtracting") != tc.wantWithheldStatement {
				t.Errorf("withheld statement present = %v, want %v", !tc.wantWithheldStatement, tc.wantWithheldStatement)
			}
			if err := Verify(raw); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}

func TestBuildCaseLongAndOddCells(t *testing.T) {
	long := strings.Repeat("a", 64<<10)
	in := CaseInput{
		Imports: []CaseImport{{Importer: "iis-httperr", SHA256: sha256Hex("f"), Rows: 50, Rejected: 30, AckText: long,
			KnownGaps: []string{long, ""}, Quality: SourceQuality{SourceVersion: strings.Repeat("v", 300)}}},
		Events: []Event{
			caseEvent("iis-httperr", "httperr", 0, map[string]string{"reason": strings.Repeat("Z", maxTokenBytes)}),
			caseEvent("iis-httperr", "httperr", 1, map[string]string{"reason": strings.Repeat("Z", maxTokenBytes+1)}),
			caseEvent("iis-httperr", "httperr", 2, map[string]string{"method": "ＧＥＴ"}), // full-width
			caseEvent("iis-httperr", "httperr", 3, map[string]string{"method": "=HYPERLINK(1)", "status": "+1", "reason": "-x"}),
			caseEvent("iis-httperr", "Request Kind", 4, nil),
		},
		Now: testNow, Key: caseKey(8),
	}
	e, raw := mustBuildCase(t, in)
	if err := Verify(raw); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	m := e.Manifest.Imports[0]
	if m.Acknowledgement != ackWithheld || m.KnownGaps[0] != gapWithheld || m.KnownGaps[1] != "" {
		t.Errorf("long free text: ack %d bytes, gaps %q", len(m.Acknowledgement), m.KnownGaps)
	}
	if e.Body.Imports[0].Quality.SourceVersion != "" {
		t.Error("an over-long quality string must be dropped")
	}
	if got := eventByID(t, e, caseEvID("iis-httperr", 0)).Attrs["reason"]; got != strings.Repeat("Z", maxTokenBytes) {
		t.Errorf("a %d-byte token is kept, got %q", maxTokenBytes, got)
	}
	for id, k := range map[int]string{1: "reason", 2: "method"} {
		if got := eventByID(t, e, caseEvID("iis-httperr", id)).Attrs[k]; got != removedText {
			t.Errorf("event %d %s = %q, want removed", id, k, got)
		}
	}
	for k, v := range eventByID(t, e, caseEvID("iis-httperr", 3)).Attrs {
		if v != removedText {
			t.Errorf("formula-like %s = %q, want removed", k, v)
		}
	}
	if got := eventByID(t, e, caseEvID("iis-httperr", 4)).Kind; got != otherKind {
		t.Errorf("kind %q, want %q", got, otherKind)
	}

	// An attribute value past the event limit is refused, not truncated.
	bad := caseEvent("iis-httperr", "httperr", 9, map[string]string{"reason": strings.Repeat("b", MaxEventAttrValue+1)})
	if _, err := BuildCase(CaseInput{Events: []Event{bad}, Now: testNow, Key: caseKey(8)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("over-long attribute: err = %v, want ErrInvalid", err)
	}
}

func TestBuildCaseRefusesBadInput(t *testing.T) {
	good := func() CaseInput {
		return CaseInput{Imports: []CaseImport{{Importer: "iis-w3c", SHA256: sha256Hex("f"), Rows: 50}},
			Events: []Event{caseEvent("iis-w3c", "request", 0, nil)}, Now: testNow, Key: caseKey(9)}
	}
	if _, err := BuildCase(good()); err != nil {
		t.Fatalf("good input: %v", err)
	}
	local := caseEvent("iis-w3c", "request", 1, nil)
	local.Start = local.Start.In(time.FixedZone("x", 3600))
	for name, mutate := range map[string]func(*CaseInput){
		"no time":           func(in *CaseInput) { in.Now = time.Time{} },
		"no key":            func(in *CaseInput) { in.Key = nil },
		"short key":         func(in *CaseInput) { in.Key = caseKey(9)[:15] },
		"importer shape":    func(in *CaseInput) { in.Imports[0].Importer = "IIS W3C" },
		"importer too long": func(in *CaseInput) { in.Imports[0].Importer = strings.Repeat("a", 33) },
		"upper-case hash":   func(in *CaseInput) { in.Imports[0].SHA256 = strings.ToUpper(sha256Hex("f")) },
		"short hash":        func(in *CaseInput) { in.Imports[0].SHA256 = "abc" },
		"negative rows":     func(in *CaseInput) { in.Imports[0].Rows = -1 },
		"negative rejected": func(in *CaseInput) { in.Imports[0].Rejected = -1 },
		"local time":        func(in *CaseInput) { in.Events[0] = local },
		"bad tz rule":       func(in *CaseInput) { in.Events[0].TZRule = "guess" },
		"line zero":         func(in *CaseInput) { in.Events[0].Line = 0 },
		"other source id":   func(in *CaseInput) { in.Events[0].EvidenceID = caseEvID("iis-httperr", 0) },
		"short id hash":     func(in *CaseInput) { in.Events[0].EvidenceID = "ev:case0001:iis-w3c:abc" },
		"upper id hash":     func(in *CaseInput) { in.Events[0].EvidenceID = "ev:case0001:iis-w3c:00000000000000AA" },
		"long case tag":     func(in *CaseInput) { in.Events[0].EvidenceID = "ev:case00012:iis-w3c:0000000000000000" },
		"not an event id":   func(in *CaseInput) { in.Events[0].EvidenceID = "p2:b1:0011223344556677" },
		"too many attrs":    func(in *CaseInput) { in.Events[0].Attrs = manyAttrs(MaxEventAttrs + 1) },
		"bad severity":      func(in *CaseInput) { in.Events[0].Severity = "fatal" },
	} {
		in := good()
		mutate(&in)
		if _, err := BuildCase(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func manyAttrs(n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("k%d", i)] = "v"
	}
	return m
}

func TestBuildCaseDeterministic(t *testing.T) {
	_, a := mustBuildCase(t, caseInput(caseKey(1)))
	in := caseInput(caseKey(1))
	for i, j := 0, len(in.Events)-1; i < j; i, j = i+1, j-1 {
		in.Events[i], in.Events[j] = in.Events[j], in.Events[i]
	}
	_, b := mustBuildCase(t, in)
	if !bytes.Equal(a, b) {
		t.Error("the same case must give the same bytes whatever the event order")
	}
}

func TestBuildCaseTruncatedStatement(t *testing.T) {
	in := caseInput(caseKey(1))
	in.EventsTruncated = true
	e, _ := mustBuildCase(t, in)
	if !hasStatementPart(e, "counts taken from it are lower bounds") {
		t.Error("missing the truncation statement")
	}
}

// Verify round trip for both schema versions, and the version rules.
func TestVerifyVersions(t *testing.T) {
	if err := Verify([]byte(goldenV1)); err != nil {
		t.Fatalf("a version 1 file must still verify: %v", err)
	}
	_, v2 := mustBuildCase(t, caseInput(caseKey(1)))
	if err := Verify(v2); err != nil {
		t.Fatalf("version 2 case export: %v", err)
	}
	_, v2b := mustBuild(t, baseInput())
	if err := Verify(v2b); err != nil {
		t.Fatalf("version 2 pattern export: %v", err)
	}

	decodeV1 := func() Export {
		var e Export
		if err := json.Unmarshal([]byte(goldenV1), &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	rehash := func(e Export) []byte {
		var err error
		if e.Manifest.BodySHA256, err = exportHash(e); err != nil {
			t.Fatal(err)
		}
		raw, err := Canonical(e)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if raw := rehash(decodeV1()); !bytes.Equal(raw, []byte(goldenV1)) {
		t.Fatal("re-encoding the version 1 golden must give the same bytes")
	}
	for name, mutate := range map[string]func(*Export){
		"v1 with events":           func(e *Export) { e.Body.Events = []ExportEvent{{EvidenceID: "x"}} },
		"v1 with body imports":     func(e *Export) { e.Body.Imports = []ExportImport{{Ref: "import-1"}} },
		"v1 with manifest imports": func(e *Export) { e.Manifest.Imports = []ManifestImport{{Ref: "import-1"}} },
		"v2 with v1 records":       func(e *Export) { e.Manifest.SchemaVersion = 2 },
		"v1 with v2 records":       func(e *Export) { e.Body.Records[0].Version = 2 },
	} {
		e := decodeV1()
		mutate(&e)
		if err := Verify(rehash(e)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A v1 file relabelled as v2 with matching record versions is a valid v2 file.
	e := decodeV1()
	e.Manifest.SchemaVersion = 2
	for i := range e.Body.Records {
		e.Body.Records[i].Version = 2
	}
	if err := Verify(rehash(e)); err != nil {
		t.Errorf("relabelled v2: %v", err)
	}

	// Tampering with an event breaks the hash; an unknown field is refused.
	for name, edit := range map[string][2]string{
		"event line":    {`"line": 3,`, `"line": 4,`},
		"unknown field": {`"tzRule": "utc-field",`, `"tzRule": "utc-field", "note": "x",`},
	} {
		bad := bytes.Replace(v2, []byte(edit[0]), []byte(edit[1]), 1)
		if bytes.Equal(bad, v2) {
			t.Fatalf("%s: edit did not apply", name)
		}
		if err := Verify(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestEventsCSV(t *testing.T) {
	neg := -5.0
	ev := caseEvent("win-counters", "counter", 0, map[string]string{"object": "Process", "counter": "% Processor Time", "instance": "_Total"})
	ev.Value = &neg
	ev.Unit = "percent-host"
	e, _ := mustBuildCase(t, CaseInput{Events: []Event{ev, caseEvent("iis-w3c", "request", 1, map[string]string{"method": "GET", "status": "-1"})}, Now: testNow, Key: caseKey(10)})
	raw := mustCSV(t, e)
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("reading the CSV back: %v", err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != "evidenceId,source,kind,host,start,end,durationMs,value,unit,severity,line,tzRule,attrs" {
		t.Fatalf("rows: %q", rows)
	}
	for _, r := range rows {
		for _, c := range r {
			if Cell(c) != c {
				t.Errorf("cell %q is not spreadsheet-safe", c)
			}
		}
	}
	first := rows[1]
	if first[7] != "'-5" || first[6] != "12.5" || first[10] != "2" || !strings.HasPrefix(first[3], "host-") {
		t.Errorf("first row: %q", first)
	}
	if first[12] != "counter=% Processor Time; instance=_Total; object=Process" {
		t.Errorf("attrs cell: %q", first[12])
	}
	if rows[2][12] != "method=GET; status=-1" {
		t.Errorf("attrs cell: %q", rows[2][12])
	}
}
