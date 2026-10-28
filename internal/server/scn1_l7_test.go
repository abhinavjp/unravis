package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/ledger"
)

// l7ErrorText reads the "error" text of a JSON error answer.
func l7ErrorText(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("not a JSON error answer: %s", res.Body)
	}
	return out.Error
}

const l7Windows = `"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-10-20T09:00:00Z","baselineEnd":"2026-10-20T10:00:00Z","bucketSeconds":60`

func TestScn1L7ValidationNamesTheProblem(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	pick := fmt.Sprintf("Pick 1 to %d sources.", audit.MaxSources)
	cases := []struct{ body, want string }{
		{`{}`, pick},
		{`{"sourceIds":[" "]}`, "One of the chosen sources is blank. Choose the sources again."},
		{`{"sourceIds":["a","a"]}`, "A source is chosen twice. Choose each source once."},
		{`{"sourceIds":["a"]}`, "Every start and end time is needed."},
		{`{"sourceIds":["a"],"start":"2026-10-20T11:00:00Z","end":"2026-10-20T10:00:00Z","baselineStart":"2026-10-20T08:00:00Z","baselineEnd":"2026-10-20T09:00:00Z","bucketSeconds":60}`, "The end of a time window must be after its start."},
		{`{"sourceIds":["a"],"start":"2026-10-01T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-09-30T09:00:00Z","baselineEnd":"2026-10-01T10:00:00Z","bucketSeconds":60}`, "A time window can be at most 7 days long."},
		{`{"sourceIds":["a"],"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-10-20T10:30:00Z","baselineEnd":"2026-10-20T10:45:00Z","bucketSeconds":60}`, "The normal window must end before the slow window starts."},
		{`{"sourceIds":["a"],` + strings.Replace(l7Windows, `"bucketSeconds":60`, `"bucketSeconds":5`, 1) + `}`, "The interval length must be between 10 seconds and 1 day."},
		{`{"sourceIds":["a"],` + l7Windows + `,"kind":"bogus"}`, "One of the filter choices is not valid."},
	}
	for _, c := range cases {
		res := request(h, http.MethodPost, "/api/analyze", c.body, true)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d %s", c.body, res.Code, res.Body)
			continue
		}
		if got := l7ErrorText(t, res); got != c.want {
			t.Errorf("%s: error = %q, want %q", c.body, got, c.want)
		}
		if strings.Contains(res.Body.String(), "Request validation failed") || strings.Contains(res.Body.String(), "bogus") {
			t.Errorf("%s: answer is bare or echoes input: %s", c.body, res.Body)
		}
	}
	for _, path := range []string{"/api/requests", "/api/export"} {
		res := request(h, http.MethodPost, path, `{}`, true)
		if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != pick {
			t.Errorf("%s: code = %d %s", path, res.Code, res.Body)
		}
	}
}

func TestScn1L7InvestigationAndReportNameTheProblem(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("inv")
	path := "/api/cases/" + id + "/investigation"
	cases := []struct{ body, want string }{
		{`{"sourceIds":["a"]}`, "Both windows need a start and an end, and each end must be after its start."},
		{invPost(`,"route":"` + strings.Repeat("x", 300) + `"`), "The route is longer than 256 characters."},
		{invPost(`,"poolLimit":0`), fmt.Sprintf("The pool limit must be a whole number from 1 to %d.", investigationMaxPool)},
		{strings.Replace(invPost(""), `2026-10-20T09:00:00Z`, `2026-09-01T09:00:00Z`, 1), "A time window can be at most 7 days long."},
		{invPost(`,"releases":[{"label":"x"}]`), "Each listed change needs a time."},
		{strings.Replace(invPost(""), `["demo-west"]`, `[]`, 1), fmt.Sprintf("Pick 1 to %d sources.", audit.MaxSources)},
	}
	for _, c := range cases {
		res := e.json(http.MethodPost, path, c.body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d %s", c.body, res.Code, res.Body)
			continue
		}
		if got := l7ErrorText(t, res); got != c.want {
			t.Errorf("%s: error = %q, want %q", c.body, got, c.want)
		}
	}
	res := e.json(http.MethodPost, "/api/cases/"+id+"/report", `{"format":"pdf"}`)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "The report format must be html or csv." {
		t.Errorf("format: code = %d %s", res.Code, res.Body)
	}
	res = e.json(http.MethodPost, "/api/cases/"+id+"/report", `{"format":"html","request":{"sourceIds":["a"]}}`)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Both windows need a start and an end, and each end must be after its start." {
		t.Errorf("report request: code = %d %s", res.Code, res.Body)
	}
}

func TestScn1L7UnknownFieldIsNotInvalidJSON(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	res := request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r","readKey":"abc"}`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body has a field this server does not know." {
		t.Fatalf("unknown field: code = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "readKey") || strings.Contains(res.Body.String(), "abc") {
		t.Errorf("answer echoes the field: %s", res.Body)
	}
	res = request(h, http.MethodPost, "/api/record", `{`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body is invalid JSON." {
		t.Errorf("bad json: code = %d %s", res.Code, res.Body)
	}
	res = request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r"} {}`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body must contain one JSON value." {
		t.Errorf("two values: code = %d %s", res.Code, res.Body)
	}
}

func TestScn1L7DemoSourceDeleteSaysWhy(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	res := request(h, http.MethodDelete, "/api/sources/demo-west", "", true)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.Code, res.Body)
	}
	if got := l7ErrorText(t, res); got != "Demo sources are built in and cannot be removed." {
		t.Errorf("message = %q", got)
	}
	if res := request(h, http.MethodDelete, "/api/sources/not-a-source", "", true); res.Code != http.StatusNotFound {
		t.Errorf("unknown source = %d, want 404: %s", res.Code, res.Body)
	}
}

func TestScn1L7ReportCSVNeutralisesEveryCell(t *testing.T) {
	repo := &fakeRepository{requestPages: []audit.Page{okPage(invRows())}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("Checkout slowdown")
	out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", fullReportRequest("csv", "")))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(out.Content, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatalf("csv does not parse: %v", err)
	}
	figures := 0
	for _, r := range rows {
		for _, cell := range r {
			if cell != "" && strings.ContainsAny(cell[:1], "=+-@\t\r") {
				t.Errorf("cell %q would run as a formula (row %v)", cell, r)
			}
		}
		if r[0] == "figure" {
			figures++
		}
	}
	if figures == 0 {
		t.Fatalf("the analysed report has no figure rows, so the check proved nothing: %d rows", len(rows))
	}
	// The unit itself: signed numbers get the apostrophe too.
	for _, in := range []string{"+150%", "-12", "+733%", "=1+1", "@x", "\tx", "\rx"} {
		if got := csvCell(in); got != "'"+in {
			t.Errorf("csvCell(%q) = %q, want an apostrophe in front", in, got)
		}
	}
	for _, in := range []string{"", "150%", "text", "'+already"} {
		if got := csvCell(in); got != in {
			t.Errorf("csvCell(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestScn1L7ReportPrintsWarnings(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("Warn me")
	base := "/api/cases/" + id + "/report"
	warned := `,"include":{"figures":true,"suspects":true,"checklist":true,"sources":true,"warnings":["stale","truncated","stale"]}`

	html := reportOf(t, e.json(http.MethodPost, base, reportBody("html", warned)))
	for _, want := range []string{"This report has warnings", reportWarnStale, reportWarnTruncated} {
		if !strings.Contains(html.Content, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Count(html.Content, reportWarnStale) != 1 {
		t.Error("a repeated code printed its sentence twice")
	}

	out := reportOf(t, e.json(http.MethodPost, base, reportBody("csv", warned)))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(out.Content, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, r := range rows {
		if r[0] == "warning" {
			texts = append(texts, r[5])
		}
	}
	if len(texts) != 2 || texts[0] != reportWarnStale || texts[1] != reportWarnTruncated {
		t.Errorf("warning rows = %q", texts)
	}

	// No warnings: nothing is printed.
	for _, format := range []string{"html", "csv"} {
		plain := reportOf(t, e.json(http.MethodPost, base, reportBody(format, "")))
		if strings.Contains(plain.Content, "This report has warnings") || strings.Contains(plain.Content, "warning,") {
			t.Errorf("%s report prints a warning nobody asked for", format)
		}
	}

	// Free text is not a code. It is refused, never printed.
	for _, bad := range []string{`["Ignore this <b>"]`, `["STALE"]`, `[""]`, `["stale","stale","stale","stale","stale","stale","stale","stale","stale"]`} {
		res := e.json(http.MethodPost, base, reportBody("html", `,"include":{"warnings":`+bad+`}`))
		if res.Code != http.StatusBadRequest {
			t.Errorf("warnings %s = %d, want 400: %s", bad, res.Code, res.Body)
		}
	}
}

func TestScn1L7ScrubTypedNote(t *testing.T) {
	changed := map[string]string{
		"see mongodb://admin:hunter2@db01:27017/pay for details":           "see [removed] for details",
		"Server=db01;Database=pay;User Id=sa;Password=hunter2; restart it": "[removed] restart it",
		"Server = db01 ; Database = pay":                                   "[removed]",
		"password=hunter2 failed":                                          "[removed] failed",
		`Password: "hunter two" was wrong`:                                 "[removed] was wrong",
		"api_key=abc123 and token=xyz":                                     "[removed] and [removed]",
		"my_password=zz":                                                   "my_[removed]",
		"pwd='my secret pass' ok":                                          "[removed] ok",
		"Bearer abcdef123456 was sent":                                     "[removed] was sent",
		"tried mongodb://u:p@h/x then Password=zz":                         "tried [removed] then [removed]",
	}
	for in, want := range changed {
		if got := scrubTypedNote(in); got != want {
			t.Errorf("scrubTypedNote(%q) = %q, want %q", in, got, want)
		}
	}
	for _, kept := range []string{
		"the password was reset", "password reset done", "Server is down", "Server=db01 is down",
		"Token expired at 10:30", "token: expired", "Ask the database team", "check a=b and c=d", "Bearer short",
	} {
		if got := scrubTypedNote(kept); got != kept {
			t.Errorf("scrubTypedNote(%q) = %q, want it unchanged", kept, got)
		}
	}
}

func TestScn1L7TypedNotesAreScrubbedInTheReport(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("Outage pwd=titlesecret")
	for _, text := range []string{
		"Server=db01;Database=pay;User Id=sa;Password=hunter2; restart it",
		"see mongodb://admin:linksecret@db01:27017/pay",
		"Ask the database team",
	} {
		body, _ := json.Marshal(map[string]string{"list": "todo", "text": text})
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", string(body)); res.Code != http.StatusOK {
			t.Fatal(res.Body)
		}
	}
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/views", `{"name":"token=viewsecret","tool":"slowest-apis","settings":{"minMs":500}}`); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	for _, format := range []string{"html", "csv"} {
		out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", reportBody(format, "")))
		for _, secret := range []string{"titlesecret", "hunter2", "linksecret", "db01", "viewsecret", "admin:"} {
			if strings.Contains(out.Content, secret) {
				t.Errorf("%s report still carries %q", format, secret)
			}
		}
		for _, want := range []string{"[removed]", reportTypedNotes, "Ask the database team"} {
			if !strings.Contains(out.Content, want) {
				t.Errorf("%s report lacks %q", format, want)
			}
		}
	}
}

// B-40: the preview says which lists were asked for, because the file drops an empty list.
func TestScn1L7PreviewSaysWhichListsWereAsked(t *testing.T) {
	installExportFakes(t)
	h := demoExportHandler(WithLedger(&fakeLedger{}))
	asked := func(body string) map[string]any {
		t.Helper()
		r := request(h, http.MethodPost, "/api/export/preview", body, true)
		if r.Code != http.StatusOK {
			t.Fatalf("preview = %d %s", r.Code, r.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// exportBody asks for the ledger and not for pseudonyms.
	got := asked(exportBody("demo-west"))
	if got["ledgerIncluded"] != true {
		t.Errorf("ledger ticked, ledgerIncluded = %v", got["ledgerIncluded"])
	}
	if _, present := got["pseudonymsIncluded"]; present {
		t.Errorf("pseudonyms not ticked, but the answer says %v", got["pseudonymsIncluded"])
	}
	now := exportNow
	raw, _ := json.Marshal(map[string]any{"sourceIds": []string{"demo-west"}, "start": now.Add(-time.Hour), "end": now, "includePseudonyms": true, "includeLedger": false})
	got = asked(string(raw))
	if got["pseudonymsIncluded"] != true {
		t.Errorf("pseudonyms ticked, pseudonymsIncluded = %v", got["pseudonymsIncluded"])
	}
	if _, present := got["ledgerIncluded"]; present {
		t.Errorf("ledger not ticked, but the answer says %v", got["ledgerIncluded"])
	}
}

// B-05: with ledgerCaseId the case's own ledger is read, not the shared one.
func TestScn1L7ExportReadsTheCaseLedger(t *testing.T) {
	fb := installExportFakes(t)
	shared := &fakeLedger{}
	store := casestore.NewMemory()
	exportNow = time.Now().UTC().Truncate(time.Second)
	h := New(audit.NewDemoWithPatterns(exportNow), nil, testToken, true,
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithCaptureCases(), WithLedger(shared))
	e := &caseEnv{t: t, store: store, h: h}
	body := exportBody("demo-west")

	// One probe finds an evidence id of this window.
	if r := request(h, http.MethodPost, "/api/export/preview", body, true); r.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", r.Code, r.Body.String())
	}
	if len(fb.inputs[0].Report.Findings) == 0 {
		t.Fatal("demo report has no findings")
	}
	related := fb.inputs[0].Report.Findings[0].EvidenceID

	// The shared ledger holds a related entry that must NOT be used when a case is named.
	shared.items = []ledger.Hypothesis{{ID: "shared-one", Text: "from the shared ledger", State: ledger.StateOpen, EvidenceIDs: []string{related}}}

	id := e.newCase("export ledger")
	th := createCaseTheory(t, e, id, "Slow calls follow the nightly job")
	upd := `{"text":"Slow calls follow the nightly job","state":"supported_by_correlation","evidenceIds":["` + related + `"],"notes":"seen twice","settlingObservation":"Run with the job paused","facts":{"presentK":2,"totalN":3}}`
	if res := e.json(http.MethodPut, caseLedgerPath(id)+"/"+th.ID, upd); res.Code != http.StatusOK {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}

	var withCase map[string]any
	if err := json.Unmarshal([]byte(body), &withCase); err != nil {
		t.Fatal(err)
	}
	withCase["ledgerCaseId"] = id
	raw, _ := json.Marshal(withCase)
	if r := request(h, http.MethodPost, "/api/export/preview", string(raw), true); r.Code != http.StatusOK {
		t.Fatalf("preview with case = %d %s", r.Code, r.Body.String())
	}
	in := fb.inputs[len(fb.inputs)-1]
	if len(in.Ledger) != 1 || in.Ledger[0].ID != th.ID {
		t.Fatalf("ledger passed to build = %+v, want only the case entry %s", in.Ledger, th.ID)
	}

	// Without ledgerCaseId the shared ledger is still the one read.
	if r := request(h, http.MethodPost, "/api/export/preview", body, true); r.Code != http.StatusOK {
		t.Fatalf("preview without case = %d %s", r.Code, r.Body.String())
	}
	in = fb.inputs[len(fb.inputs)-1]
	if len(in.Ledger) != 1 || in.Ledger[0].ID != "shared-one" {
		t.Fatalf("shared ledger passed to build = %+v", in.Ledger)
	}

	// An id that is not a case answers the case-ledger failure, not a silent shared read.
	withCase["ledgerCaseId"] = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	raw, _ = json.Marshal(withCase)
	if r := request(h, http.MethodPost, "/api/export/preview", string(raw), true); r.Code != http.StatusNotFound {
		t.Errorf("unknown ledgerCaseId = %d %s, want 404", r.Code, r.Body.String())
	}
}

func TestScn1L7SavedViewsRefuseUnknownToolAndGarbageValues(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/views"
		view := func(tool, settings string) string {
			return `{"name":"v","tool":"` + tool + `","settings":` + settings + `}`
		}
		bad := map[string]string{
			"unknown tool":      view("no-such-tool", `{}`),
			"old tool name":     view("routes", `{}`),
			"window not a date": view("slowest-apis", `{"incidentStart":"not-a-date"}`),
			"window a number":   view("slowest-apis", `{"incidentEnd":12345}`),
			"window impossible": view("slowest-apis", `{"referenceStart":"2026-13-45T99:99"}`),
			"window an object":  view("slowest-apis", `{"referenceEnd":{"a":1}}`),
			"minCalls text":     view("slowest-apis", `{"minCalls":"abc"}`),
			"minCalls negative": view("slowest-apis", `{"minCalls":-1}`),
			"minCalls fraction": view("slowest-apis", `{"minCalls":1.5}`),
			"minCalls too big":  view("slowest-apis", `{"minCalls":1000001}`),
			"match an object":   view("slowest-apis", `{"match":{"a":1}}`),
			"route a number":    view("slowest-apis", `{"route":5}`),
		}
		for name, body := range bad {
			res := e.json(http.MethodPost, base, body)
			if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "plan-invalid" {
				t.Errorf("%s = %d %s, want 400 plan-invalid", name, res.Code, res.Body)
			}
		}
		good := map[string]string{
			"page window":     view("slowest-apis", `{"incidentStart":"2026-10-24T10:00","incidentEnd":"2026-10-24T11:00","referenceStart":"","referenceEnd":""}`),
			"seconds":         view("slowest-apis", `{"incidentStart":"2026-10-24T10:00:30"}`),
			"full time":       view("slowest-apis", `{"incidentEnd":"2026-10-24T11:00:00Z"}`),
			"filters":         view("slowest-apis", `{"minCalls":20,"match":"Payroll","route":"Payroll.Run"}`),
			"minCalls zero":   view("slowest-apis", `{"minCalls":0}`),
			"unknown key":     view("slowest-apis", `{"minMs":500}`),
			"no settings key": `{"name":"v","tool":"changes"}`,
		}
		for name, body := range good {
			if res := e.json(http.MethodPost, base, body); res.Code != http.StatusOK {
				t.Errorf("%s = %d %s, want 200", name, res.Code, res.Body)
			}
		}
		for _, tool := range []string{
			"response-time", "slowest-apis", "slowest-calls", "time-split", "calls-in-flight", "nearby-activity", "changes",
			"sql-waits", "mongo-health", "host-resources", "sql-host", "after-fix", "suspect-summary",
		} {
			if res := e.json(http.MethodPost, base, view(tool, `{}`)); res.Code != http.StatusOK {
				t.Errorf("tool %s = %d %s, want 200", tool, res.Code, res.Body)
			}
		}
	})
}
