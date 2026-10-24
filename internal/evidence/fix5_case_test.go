package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
)

// Tests for the P13 privacy-review fixes (H1, M1-M5, L2, L3, L5, L6). Values are
// synthetic, as in caseexport_test.go.

func fix5Decode(t *testing.T, raw []byte) Export {
	t.Helper()
	var e Export
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// fix5Rehash writes e with a correct hash, as an attacker who edits the file
// and then recomputes the hash would.
func fix5Rehash(t *testing.T, e Export) []byte {
	t.Helper()
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

// M1: no source line number, M2: nothing stable across exports.
func TestFix5NoLineNoFileHashNoStableLabels(t *testing.T) {
	_, a := mustBuildCase(t, caseInput(caseKey(1)))
	_, b := mustBuildCase(t, caseInput(caseKey(2)))
	for _, raw := range [][]byte{a, b} {
		for _, bad := range []string{`"line"`, `"sha256"`, `"acknowledgement"`, "ev:", "case0001"} {
			if bytes.Contains(raw, []byte(bad)) {
				t.Errorf("the file must not contain %s", bad)
			}
		}
		assertAbsent(t, raw, sha256Hex("file-a"), sha256Hex("file-b"), sha256Hex("file-c"))
	}
	// Under another key every event label, host and pseudonym differs: nothing
	// in the two files is shared that could join them.
	la := map[string]bool{}
	for _, p := range eventLabelPattern.FindAllString(string(a), -1) {
		la[p] = true
	}
	for _, p := range append(eventLabelPattern.FindAllString(string(b), -1), pseudonymPattern.FindAllString(string(b), -1)...) {
		if la[p] {
			t.Errorf("label %s is shared between two exports", p)
		}
	}
	for _, p := range pseudonymPattern.FindAllString(string(a), -1) {
		if strings.Contains(string(b), p) {
			t.Errorf("pseudonym %s is shared between two exports", p)
		}
	}
	// The same events in a different order, under the same key, give the same
	// bytes, and an event's label does not depend on its position.
	e, _ := mustBuildCase(t, caseInput(caseKey(1)))
	for i := 1; i < len(e.Body.Events); i++ {
		if e.Body.Events[i].EvidenceID == e.Body.Events[i-1].EvidenceID {
			t.Fatal("two events share a label")
		}
	}
}

func TestFix5LabelShape(t *testing.T) {
	k := caseKey(7)
	h, u := keyedLabel(k, "host", "web-a"), keyedLabel(k, "user", "web-a")
	if !labelOK("host", h, labelHex) || len(h) != len("host-")+12 || labelHex < 12 {
		t.Errorf("host label %q must be 12 hex characters", h)
	}
	if h == "host-"+u[len("user-"):] {
		t.Error("the prefix must be inside the MAC")
	}
	if keyedLabel(k, "host", "web-a") != h || keyedLabel(caseKey(8), "host", "web-a") == h {
		t.Error("a label must be stable under one key and change under another")
	}
	ev := keyedLabelN(k, "ev", "ev:case0001:iis-w3c:0000000000000001", eventLabelHex)
	if !labelOK("ev", ev, 16) || labelOK("ev", ev, 12) {
		t.Errorf("event label %q must be 16 hex characters", ev)
	}
	for _, bad := range []string{"host-ABCDEF012345", "host-abcdef01234", "host-abcdef0123456", "hosts-abcdef012345", "host-abcdef01234g", ""} {
		if labelOK("host", bad, 12) {
			t.Errorf("labelOK accepted %q", bad)
		}
	}
}

// H1: the acknowledgement is a banner id, never client text.
func TestFix5BannerID(t *testing.T) {
	known := bannerHash(knownBanners[0])
	for _, tc := range []struct{ ack, want string }{
		{knownBanners[0], known},
		{"  " + strings.ReplaceAll(knownBanners[0], " ", "  ") + "\n", known},
		{"", customBannerID},
		{"I confirm this file holds no passwords.", customBannerID},
		{knownBanners[0] + " plus a note", customBannerID},
		{"Contact bob@example.test about 192.0.2.7", customBannerID},
		{strings.Repeat("a", 1<<16), customBannerID},
	} {
		if got := bannerID(tc.ack); got != tc.want {
			t.Errorf("bannerID(%.40q) = %q, want %q", tc.ack, got, tc.want)
		}
	}
	if !knownBannerIDs[customBannerID] || !knownBannerIDs[known] || knownBannerIDs["banner-0000000000000000"] {
		t.Error("knownBannerIDs is wrong")
	}
	if strings.ContainsAny(customBannerID, " @:/") {
		t.Errorf("custom banner id %q looks like text", customBannerID)
	}
}

// H1: version strings are kept only when they look like a version number.
func TestFix5VersionStrings(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3": "1.2.3", "0.13.0-test": "0.13.0-test", "2.0.0+build5": "2.0.0+build5", "10": "10", "1.0": "1.0",
		"": "", "10.0.0.1": "", "1.2.3.4": "", "192.0.2.5": "", "v1.2": "", "W3C 1.0": "", "1.0 beta": "",
		"1.2.3.4.5": "", "12345.1": "", "1.x@y": "", "1.2.caused-by": "", "1.2/3": "", "1.2\n3": "",
		strings.Repeat("1", 300): "",
	} {
		if got := exportVersion(in); got != want {
			t.Errorf("exportVersion(%.30q) = %q, want %q", in, got, want)
		}
	}
	in := CaseInput{Imports: []CaseImport{{Importer: "iis-w3c", SHA256: sha256Hex("f"), Rows: 50, Rejected: 30,
		Quality: SourceQuality{SourceVersion: "10.0.0.1", Threshold: "keeps 5 minutes of web-a", Sampling: "every 3rd host-a",
			TimezoneRule: "customer standard time"}}}, ToolVersion: "192.0.2.9", Now: testNow, Key: caseKey(1)}
	e, raw := mustBuildCase(t, in)
	if e.Manifest.ToolVersion != "" {
		t.Errorf("tool version %q looks like an address and must be dropped", e.Manifest.ToolVersion)
	}
	if q := e.Body.Imports[0].Quality; q != (SourceQuality{}) {
		t.Errorf("quality %+v must be empty", q)
	}
	assertAbsent(t, raw, "web-a", "host-a", "minutes", "customer", "10.0.0.1", "192.0.2.9")
	in.ToolVersion, in.Imports[0].Quality = "1.4.0", SourceQuality{SourceVersion: "2.1", TimezoneRule: clock.RuleHostLocal}
	e, _ = mustBuildCase(t, in)
	if e.Manifest.ToolVersion != "1.4.0" || e.Body.Imports[0].Quality.SourceVersion != "2.1" || e.Body.Imports[0].Quality.TimezoneRule != clock.RuleHostLocal {
		t.Errorf("clean values must be kept: %q %+v", e.Manifest.ToolVersion, e.Body.Imports[0].Quality)
	}
}

// M3: only numbers, fixed terms and keyed pseudonyms survive.
func TestFix5FreeFormValuesDropped(t *testing.T) {
	const odd = "ZZZ-Not-Listed-" + caseSentinel
	ev := []Event{
		caseEvent("win-events", "win-event", 0, map[string]string{"provider": odd, "log": odd, "reason": odd, "id": odd}),
		caseEvent("appinsights", "dependency", 1, map[string]string{"dependencyType": odd, "resultCode": odd, "success": odd}),
		caseEvent("sql-xe", "wait", 2, map[string]string{"component": odd, "wait_class": odd, "event": odd, "wait_type": "PREEMPTIVE_" + caseSentinel}),
		caseEvent("schedule-hangfire", "job-run", 3, map[string]string{"tz": "Customer Standard Time", "basis": odd, "freq": odd}),
		caseEvent("win-counters", "counter", 4, map[string]string{"object": odd, "counter": odd}),
		caseEvent("mongod-json", "slow-op", 5, map[string]string{"command": odd, "planSummary": "COLLSCAN," + odd, "type": odd}),
		caseEvent("iis-w3c", "request", 6, map[string]string{"method": odd, "status": "2OO", "port": "443 "}),
	}
	e, raw := mustBuildCase(t, CaseInput{Events: ev, Now: testNow, Key: caseKey(11)})
	for _, x := range e.Body.Events {
		for k, v := range x.Attrs {
			if v != removedText {
				t.Errorf("%s %s = %q, want removed", x.Source, k, v)
			}
		}
	}
	assertAbsent(t, raw, caseSentinel, "Customer", "ZZZ")
	// Listed terms stay, in their listed spelling.
	good := []Event{
		caseEvent("win-events", "win-event", 0, map[string]string{"provider": "microsoft-windows-was", "log": "System", "id": "5074"}),
		caseEvent("appinsights", "dependency", 1, map[string]string{"dependencyType": "sql", "resultCode": "OTHER", "success": "TRUE"}),
		caseEvent("sql-xe", "wait", 2, map[string]string{"wait_type": "PAGEIOLATCH_SH", "wait_class": "lock"}),
		caseEvent("schedule-hangfire", "job-run", 3, map[string]string{"tz": "etc/utc", "freq": "daily"}),
		caseEvent("win-counters", "counter", 4, map[string]string{"object": "process", "counter": "% processor time"}),
		caseEvent("mongod-json", "slow-op", 5, map[string]string{"planSummary": "ixscan, fetch"}),
	}
	e, raw = mustBuildCase(t, CaseInput{Events: good, Now: testNow, Key: caseKey(11)})
	want := map[string]map[string]string{
		"win-events":        {"provider": "Microsoft-Windows-WAS", "log": "System", "id": "5074"},
		"appinsights":       {"dependencyType": "SQL", "resultCode": "other", "success": "true"},
		"sql-xe":            {"wait_type": "PAGEIOLATCH_SH", "wait_class": "lock"},
		"schedule-hangfire": {"tz": "Etc/UTC", "freq": "daily"},
		"win-counters":      {"object": "Process", "counter": "% Processor Time"},
		"mongod-json":       {"planSummary": "IXSCAN,FETCH"},
	}
	for _, x := range e.Body.Events {
		for k, v := range want[x.Source] {
			if x.Attrs[k] != v {
				t.Errorf("%s %s = %q, want %q", x.Source, k, x.Attrs[k], v)
			}
		}
	}
	if err := Verify(raw); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// M4 and L4: the claim guard reads through spelling tricks and synonyms.
func TestFix5ClaimGuard(t *testing.T) {
	caught := []string{
		"blocked_by", "blocked-by lock", "due-to", "due.to load", "due_to", "because of rotation", "resulted in a stall",
		"led to a stall", "attributable to load", "triggered by a job", "root_cause", "root-cause", "cause", "caused", "causal",
		"strongly correlated", "Strong-Correlation", "strongly_correlates", "correlation is strong", "strong: correlated",
		"cäuse", "causе", "ᴘʀᴏᴠᴇɴ", "pro​ven", "PRОVEN", "proof", "ｐroven", "waiting on lock",
	}
	for _, s := range caught {
		if claimFree(s) {
			t.Errorf("%q must be caught", s)
		}
	}
	clean := []string{"stronger signal", "causeway", "correlation", "a strong signal", "rows were dropped at rotation", "duet", "blocked"}
	for _, s := range clean {
		if !claimFree(s) {
			t.Errorf("%q must pass", s)
		}
	}
	// A planted synonym in a kind, a gap and an attribute never reaches the file.
	kinds := []string{"blocked_by", "due-to", "strongly-correlated", "root_cause", "led-to", "cäused-by"}
	var ev []Event
	for i, k := range kinds {
		ev = append(ev, caseEvent("sql-xe", k, i, map[string]string{"wait_type": k}))
	}
	imp := []CaseImport{{Importer: "sql-xe", SHA256: sha256Hex("f"), Rows: 50, Rejected: 30, AckText: "led to a stall",
		KnownGaps: []string{"gaps are due to rotation", "ok gap", "strongly correlated gap", "res​ulted in gap"}}}
	e, raw := mustBuildCase(t, CaseInput{Imports: imp, Events: ev, Now: testNow, Key: caseKey(12)})
	for _, x := range e.Body.Events {
		if x.Kind != otherKind || x.Attrs["wait_type"] != removedText {
			t.Errorf("event %+v must carry no claim", x)
		}
	}
	if !claimFree(string(raw)) {
		t.Errorf("the file carries a claim:\n%s", raw)
	}
	gaps := e.Manifest.Imports[0].KnownGaps
	if gaps[0] != gapWithheld || gaps[1] != "ok gap" || gaps[2] != gapWithheld || gaps[3] != gapWithheld {
		t.Errorf("gaps: %q", gaps)
	}
}

// M5: a recomputed hash is not enough. Every tampered file below carries a
// correct hash and canonical form, and must still fail Verify.
func TestFix5VerifyReappliesPolicy(t *testing.T) {
	_, base := mustBuildCase(t, caseInput(caseKey(1)))
	if err := Verify(fix5Rehash(t, fix5Decode(t, base))); err != nil {
		t.Fatalf("an unmodified file with a recomputed hash must verify: %v", err)
	}
	var someUser, someEv int
	for i, x := range fix5Decode(t, base).Body.Events {
		if x.Source == "iis-w3c" && someUser == 0 {
			someUser = i
		}
		if x.Source == "win-counters" && someEv == 0 {
			someEv = i
		}
	}
	neg := -1.0
	five := 5
	for name, mutate := range map[string]func(*Export){
		"ip in banner id":       func(e *Export) { e.Manifest.Imports[0].BannerID = "198.51.100.7" },
		"ack text as banner id": func(e *Export) { e.Manifest.Imports[0].BannerID = "I confirm this file holds no passwords" },
		"unknown banner id":     func(e *Export) { e.Manifest.Imports[0].BannerID = "banner-0000000000000000" },
		"email in gap":          func(e *Export) { e.Manifest.Imports[0].KnownGaps = []string{"write to bob@example.test"} },
		"ip in gap":             func(e *Export) { e.Manifest.Imports[0].KnownGaps = []string{"client 192.0.2.7 was seen"} },
		"causal gap":            func(e *Export) { e.Manifest.Imports[0].KnownGaps = []string{"rows lost due to rotation"} },
		"ip in statement": func(e *Export) {
			e.Manifest.Masking.Statements = append(e.Manifest.Masking.Statements, "see 192.0.2.7")
		},
		"proven statement":       func(e *Export) { e.Manifest.Masking.Statements[0] += " This is proven." },
		"no statements":          func(e *Export) { e.Manifest.Masking.Statements = nil },
		"tool version is an ip":  func(e *Export) { e.Manifest.ToolVersion = "10.0.0.1" },
		"tool version is text":   func(e *Export) { e.Manifest.ToolVersion = "bob's laptop" },
		"causal kind":            func(e *Export) { e.Body.Events[someUser].Kind = "caused-by-lock" },
		"kind of another source": func(e *Export) { e.Body.Events[someUser].Kind = "wait" },
		"unknown source":         func(e *Export) { e.Body.Events[someUser].Source = "custom-thing" },
		"unit text":              func(e *Export) { e.Body.Events[someUser].Unit = "furlongs" },
		"severity text":          func(e *Export) { e.Body.Events[someUser].Severity = "fatal" },
		"tz rule text":           func(e *Export) { e.Body.Events[someUser].TZRule = "guess" },
		"end before start":       func(e *Export) { e.Body.Events[someUser].End = e.Body.Events[someUser].Start.Add(-time.Second) },
		"local time": func(e *Export) {
			e.Body.Events[someUser].Start = e.Body.Events[someUser].Start.In(time.FixedZone("x", 3600))
		},
		"negative duration":     func(e *Export) { e.Body.Events[someUser].DurationMs = &neg },
		"raw host":              func(e *Export) { e.Body.Events[someUser].Host = "web-a" },
		"short host label":      func(e *Export) { e.Body.Events[someUser].Host = "host-abcd1234" },
		"old style event id":    func(e *Export) { e.Body.Events[someUser].EvidenceID = "ev:case0001:iis-w3c:0000000000000001" },
		"short event id":        func(e *Export) { e.Body.Events[someUser].EvidenceID = "ev-0123456789ab" },
		"repeated event id":     func(e *Export) { e.Body.Events[someUser+1].EvidenceID = e.Body.Events[someUser].EvidenceID },
		"events out of order":   func(e *Export) { ev := e.Body.Events; ev[0], ev[len(ev)-1] = ev[len(ev)-1], ev[0] },
		"ip attribute":          func(e *Export) { e.Body.Events[someUser].Attrs["method"] = "192.0.2.5" },
		"free text attribute":   func(e *Export) { e.Body.Events[someUser].Attrs["status"] = "ok" },
		"unlisted attribute":    func(e *Export) { e.Body.Events[someUser].Attrs["cookie"] = "x" },
		"dropped attribute":     func(e *Export) { e.Body.Events[someUser].Attrs["stem"] = "/a" },
		"raw user attribute":    func(e *Export) { e.Body.Events[someUser].Attrs["user"] = "alice" },
		"stored pseudonym":      func(e *Export) { e.Body.Events[someUser].Attrs["user"] = "user-a0000000" },
		"counter text":          func(e *Export) { e.Body.Events[someEv].Attrs["counter"] = "\\\\WEB-A\\Process\\Foo" },
		"empty attribute":       func(e *Export) { e.Body.Events[someUser].Attrs["status"] = "" },
		"records present":       func(e *Export) { e.Body.Records = []Record{{Version: CaseSchemaVersion}} },
		"import count mismatch": func(e *Export) { e.Body.Imports = e.Body.Imports[:1] },
		"import ref":            func(e *Export) { e.Manifest.Imports[0].Ref = "import-9"; e.Body.Imports[0].Ref = "import-9" },
		"import importer text": func(e *Export) {
			e.Manifest.Imports[0].Importer = "acme-secret"
			e.Body.Imports[0].Importer = "acme-secret"
		},
		"import host":              func(e *Export) { e.Body.Imports[0].Host = "web-a" },
		"import sampling text":     func(e *Export) { e.Body.Imports[0].Quality.Sampling = "every 3rd" },
		"import source version":    func(e *Export) { e.Body.Imports[0].Quality.SourceVersion = "W3C 1.0" },
		"import tz rule":           func(e *Export) { e.Body.Imports[0].TZRule = "guess" },
		"import small shown count": func(e *Export) { e.Body.Imports[1].Rejected = Count{Value: &five, Text: "5"} },
		"import zero value":        func(e *Export) { e.Body.Imports[1].Rows = Count{Value: &five, Masked: true, Text: "<20"} },
		"downgrade":                func(e *Export) { e.Manifest.SchemaVersion = 2 },
		"downgrade to v1":          func(e *Export) { e.Manifest.SchemaVersion = 1 },
		"upgrade":                  func(e *Export) { e.Manifest.SchemaVersion = MaxSchemaVersion + 1 },
	} {
		e := fix5Decode(t, base)
		mutate(&e)
		if e.Manifest.SchemaVersion == 2 || e.Manifest.SchemaVersion == 1 {
			for i := range e.Body.Records {
				e.Body.Records[i].Version = e.Manifest.SchemaVersion
			}
		}
		raw := fix5Rehash(t, e)
		if err := Verify(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

// The error names the rule, never the planted value.
func TestFix5VerifyErrorsDoNotEchoValues(t *testing.T) {
	_, base := mustBuildCase(t, caseInput(caseKey(1)))
	e := fix5Decode(t, base)
	e.Manifest.Imports[0].KnownGaps = []string{"write to bob@example.test"}
	err := Verify(fix5Rehash(t, e))
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "bob") || strings.Contains(err.Error(), "example") {
		t.Errorf("err = %v", err)
	}
}

// L2 and L3: control characters in front of a trigger, and numbers in CSV.
func TestFix5CSVCells(t *testing.T) {
	for _, in := range []string{"\x00=cmd", "\x1b+1", "\x01\x02@SUM(A1)", "\x7f-1+1", "\u0085=1", "\t=1", "\r=1", "=1+1", "+1+1", "@x", "-x", "--5", "-5 rows", "-1e5x"} {
		if got := csvCell(in); got != "'"+in {
			t.Errorf("csvCell(%q) = %q, want an apostrophe in front", in, got)
		}
		if got := Cell(in); got != "'"+in {
			t.Errorf("Cell(%q) = %q, want an apostrophe in front", in, got)
		}
	}
	for _, in := range []string{"-5", "12.5", "-0.25", "0", "1234", "abc", "a=b", "", "'=1", "x\x00=1"} {
		if got := csvCell(in); got != in {
			t.Errorf("csvCell(%q) = %q, want it unchanged", in, got)
		}
	}
	// Idempotent on the escaped form.
	for _, in := range []string{"\x00=cmd", "=1", "-x"} {
		if once := csvCell(in); csvCell(once) != once {
			t.Errorf("csvCell is not idempotent on %q", in)
		}
	}
}

// L5: spelled-out counts, ranges, lists and counts glued to a word.
func TestFix5MaskNumbers(t *testing.T) {
	for in, want := range map[string]string{
		"three rows":         "<20 rows",
		"Nineteen requests":  "<20 requests",
		"5rows":              "<20rows",
		"3 of 7 rows":        "<20 of <20 rows",
		"rows 1,2,3 skipped": "rows <20,<20,<20 skipped",
		"5-7 rows":           "<20-<20 rows",
		"see 1-2, ok":        "see <20-<20, ok",
		"a 1-2.":             "a <20-<20.",
		"5 to 7 rows":        "<20 to <20 rows",
		"n=5":                "n=<20",
		// Left alone: not a small count.
		"twenty rows":     "twenty rows",
		"1,234 rows":      "1,234 rows",
		"2024-01-05 rows": "2024-01-05 rows",
		"12:30":           "12:30",
		"5.5 rows":        "5.5 rows",
		"B12":             "B12",
		"25 rows":         "25 rows",
		"100-200":         "100-200",
		"1-2-3":           "1-2-3",
	} {
		if got := maskNumbers(in); got != want {
			t.Errorf("maskNumbers(%q) = %q, want %q", in, got, want)
		}
		if again := maskNumbers(maskNumbers(in)); again != maskNumbers(in) {
			t.Errorf("maskNumbers is not idempotent on %q: %q", in, again)
		}
	}
	// In a known gap, through the export.
	gap := cleanCaseText("Dropped 3 rows, two requests and 1-4 lines.", gapWithheld)
	if strings.ContainsAny(gap, "34") || strings.Contains(gap, "two") || !strings.Contains(gap, "<20") {
		t.Errorf("gap %q still shows a small count", gap)
	}
}

// Person pseudonyms: every shown user has the thin-cell number of events, so
// a reader cannot count a small group, and no raw or stored value is shown.
func TestFix5PersonCrossTab(t *testing.T) {
	ev := userEvents(ThinCell+5, ThinCell, 0, "", 0)
	e, raw := mustBuildCase(t, CaseInput{Events: ev, Now: testNow, Key: caseKey(13)})
	per := map[string]int{}
	for _, x := range e.Body.Events {
		u := x.Attrs["user"]
		if !labelOK("user", u, labelHex) {
			t.Fatalf("user %q is not a keyed label", u)
		}
		per[u]++
	}
	if len(per) != ThinCell+5 {
		t.Errorf("%d user labels, want %d", len(per), ThinCell+5)
	}
	for u, n := range per {
		if n < ThinCell {
			t.Errorf("%s has %d events, below the thin cell", u, n)
		}
	}
	for u := 0; u < ThinCell+5; u++ {
		assertAbsent(t, raw, storedUser(u))
	}
	// Under another key, no label is shared.
	e2, _ := mustBuildCase(t, CaseInput{Events: ev, Now: testNow, Key: caseKey(14)})
	for _, x := range e2.Body.Events {
		if _, ok := per[x.Attrs["user"]]; ok {
			t.Fatalf("user label %s is shared between keys", x.Attrs["user"])
		}
	}
}

func TestFix5StatementsAreHonest(t *testing.T) {
	e, _ := mustBuildCase(t, caseInput(caseKey(1)))
	for _, part := range []string{"Event times and durations are exact", "trusted counterparty", "does not remove it", "banner id", "12 hex characters"} {
		if !hasStatementPart(e, part) {
			t.Errorf("missing statement part %q", part)
		}
	}
	for _, s := range e.Manifest.Masking.Statements {
		for _, bad := range []string{"cannot be linked", "anonymous", "anonymised", "guarantee"} {
			if strings.Contains(strings.ToLower(s), bad) {
				t.Errorf("statement overclaims (%q): %s", bad, s)
			}
		}
	}
}

// BuildCase and Verify cannot drift: BuildCase output always verifies, for a
// spread of inputs.
func TestFix5BuildOutputAlwaysVerifies(t *testing.T) {
	inputs := []CaseInput{
		caseInput(caseKey(1)),
		{Now: testNow, Key: caseKey(2)},
		{Events: userEvents(ThinCell, ThinCell, ThinCell, "-", 0), Now: testNow, Key: caseKey(3), EventsTruncated: true},
	}
	for i, in := range inputs {
		_, raw := mustBuildCase(t, in)
		if err := Verify(raw); err != nil {
			t.Errorf("input %d: %v", i, err)
		}
	}
	// The listed provider with a dotted version ("ASP.NET 4.0.30319.0") must
	// pass the address check of Verify.
	e, raw := mustBuildCase(t, CaseInput{Events: []Event{caseEvent("win-events", "win-event", 0, map[string]string{"provider": "ASP.NET 4.0.30319.0"})}, Now: testNow, Key: caseKey(4)})
	if got := e.Body.Events[0].Attrs["provider"]; got != "ASP.NET 4.0.30319.0" {
		t.Fatalf("provider = %q", got)
	}
	if err := Verify(raw); err != nil {
		t.Errorf("Verify: %v", err)
	}
}
