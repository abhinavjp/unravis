package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/ledger"
)

// Integration of the whole importer wave behind the HTTP surface: the real
// importer registry, the real encrypted SQLite case store (with a test
// protector), the real evidence engine and a real ledger. Every file here is
// synthetic.

const intHostsDoc = `{"version":1,"hosts":[
 {"label":"web-a","aliases":["web-a.example.test"],"role":"iis","tz":"UTC"},
 {"label":"mongo-a","aliases":["mongo-a.example.test"],"role":"mongo","tz":"UTC"}]}`

// intHTTPERR and intMongod are small synthetic logs. The words in sentinel
// are client data: no response and no export may carry them.
const (
	intHTTPERR = "#Software: Microsoft HTTP API 2.0\n#Version: 1.0\n#Date: 2001-03-17 00:00:00\n" +
		"#Fields: date time c-ip c-port s-ip s-port cs-version cs-method cs-uri sc-status s-siteid s-reason s-queuename\n" +
		"2001-03-17 10:00:01 192.0.2.50 51000 192.0.2.10 443 HTTP/1.1 GET /api/v1/orders/42?q=SENTINEL-int-91 503 1 QueueFull DefaultAppPool\n" +
		"2001-03-17 10:00:02 192.0.2.51 51001 192.0.2.10 443 HTTP/1.1 - - - - Connection_Dropped -\n" +
		"2001-03-17 10:00:04 192.0.2.53 51003 192.0.2.10 443 HTTP/1.1 GET /x 408 1 timer_connectionidle pool-alpha\n"
	intMongod = `{"t":{"$date":"2001-03-17T10:00:00.000+00:00"},"s":"I","c":"CONTROL","id":4615611,"ctx":"initandlisten","msg":"MongoDB starting","attr":{"pid":4242,"port":27017,"host":"mongo-a.example.test"}}` + "\n" +
		`{"t":{"$date":"2001-03-17T10:00:02.000+00:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn12","msg":"Slow query","attr":{"type":"command","ns":"app.orders","appName":"SENTINEL-int-91","command":{"find":"orders","filter":{"customer":"SENTINEL-int-91"},"$db":"app"},"planSummary":"COLLSCAN","keysExamined":0,"docsExamined":5000,"nreturned":3,"numYields":5,"queryHash":"0A0B0C0D","remote":"192.0.2.10:51234","durationMillis":2500}}` + "\n"
	intFileName = "synthetic-private-name-int.log"
	intSentinel = "SENTINEL-int-91"
)

type intEnv struct {
	*caseEnv
	root string
}

func newIntEnv(t *testing.T, repo *fakeRepository) *intEnv {
	t.Helper()
	led, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := sqlite.New(sqlite.Options{
		Root: root, Protector: storetest.RandomProtector(), MinFreeBytes: -1,
		// The same hook the dashboard wires in main: strip the ledger on erase.
		OnErase: func(_ context.Context, id casestore.CaseID) error {
			_, err := led.(ledger.Stripper).StripEvidence(evidence.CasePrefixes(string(id)), "erased-case-evidence")
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	e := &caseEnv{t: t, store: store, ledger: led}
	e.h = New(repo, testAssets(), testToken, false,
		WithCases(store, mustHosts(t, intHostsDoc), all.Registry()), WithLedger(led))
	return &intEnv{caseEnv: e, root: root}
}

func (e *intEnv) commit(id, importerName, host, content string) {
	e.t.Helper()
	path := "/api/cases/" + id + "/imports?importer=" + importerName + "&host=" + host
	res := e.upload(path, ackFields, &upload{intFileName, content})
	if res.Code != http.StatusCreated {
		e.t.Fatalf("commit %s = %d %s", importerName, res.Code, res.Body)
	}
}

func intRepo() *fakeRepository {
	start := time.Date(2001, 3, 17, 10, 0, 0, 0, time.UTC)
	ms := 5000.0
	row := audit.RequestRecord{ID: "req-int-1", SourceID: "src", InitiatedAt: &start, AuditCreateTimeMs: &ms}
	return &fakeRepository{requestPages: []audit.Page{{Rows: []audit.RequestRecord{row}}}}
}

const intTimelineBody = `{"query":{"sourceIds":["src"],"start":"2001-03-17T09:00:00Z","end":"2001-03-17T11:00:00Z",` +
	`"baselineStart":"2001-03-17T08:00:00Z","baselineEnd":"2001-03-17T09:00:00Z","bucketSeconds":60}}`

func TestIntegrationImportTimelineExportErase(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Synthetic incident")

	// Preview: detection with the real registry, nothing stored.
	res := e.upload("/api/cases/"+id+"/imports?preview=1&host=web-a", nil, &upload{intFileName, intHTTPERR})
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var detected struct {
		Importer string `json:"importer"`
	}
	decodeBody(t, res, &detected)
	if detected.Importer != "iis-httperr" {
		t.Fatalf("detected %q (%s)", detected.Importer, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("preview stored %d imports", n)
	}

	// One source is not enough for a timeline: it is disabled, with a reason.
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	var view struct {
		Enabled    bool   `json:"enabled"`
		ReasonCode string `json:"reasonCode"`
		Reason     string `json:"reason"`
	}
	res = e.json(http.MethodPost, "/api/cases/"+id+"/timeline", intTimelineBody)
	if res.Code != http.StatusOK {
		t.Fatalf("timeline(1 source) = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &view)
	if view.Enabled || view.Reason == "" || view.ReasonCode == "" {
		t.Fatalf("one source should disable the timeline: %s", res.Body)
	}

	// The second source enables it: the time zone rule of each import was
	// derived from the importer's own report, not left empty.
	e.commit(id, "mongod-json", "mongo-a", intMongod)
	res = e.json(http.MethodPost, "/api/cases/"+id+"/timeline", intTimelineBody)
	if res.Code != http.StatusOK {
		t.Fatalf("timeline(2 sources) = %d %s", res.Code, res.Body)
	}
	var full struct {
		Enabled   bool `json:"enabled"`
		Reason    string
		Intervals int `json:"intervals"`
		Rows      []struct {
			Kind string `json:"kind"`
		} `json:"rows"`
		Sources []struct {
			Name   string `json:"name"`
			TZRules []string `json:"tzRules"`
		} `json:"sources"`
	}
	decodeBody(t, res, &full)
	if !full.Enabled || full.Intervals != 1 || len(full.Rows) < 2 || len(full.Sources) != 2 {
		t.Fatalf("timeline = %s", res.Body)
	}
	for _, s := range full.Sources {
		if len(s.TZRules) == 0 {
			t.Errorf("source %q has no time zone rule: %s", s.Name, res.Body)
		}
	}
	for _, imp := range e.importsOf(id) {
		if imp.TZRule == "" {
			t.Errorf("import %d (%s) stored no time zone rule", imp.ID, imp.Importer)
		}
	}
	if strings.Contains(res.Body.String(), intSentinel) || strings.Contains(res.Body.String(), intFileName) {
		t.Errorf("timeline carries client data or a file name: %s", res.Body)
	}

	// Case export: the redacted JSON and the events CSV.
	res = e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("case export preview = %d %s", res.Code, res.Body)
	}
	var preview exportPreviewResponse
	decodeBody(t, res, &preview)
	if preview.PreviewID == "" {
		t.Fatalf("no preview id: %s", res.Body)
	}
	res = e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+preview.PreviewID+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", res.Code, res.Body)
	}
	var confirmed exportConfirmResponse
	decodeBody(t, res, &confirmed)
	if confirmed.Content == "" || confirmed.EventsCSV == "" || confirmed.EventsCSVFilename == "" || confirmed.SHA256 == "" || confirmed.EventsCSVSHA256 == "" {
		t.Fatalf("confirm lacks a file: %+v", confirmed)
	}
	if sha(confirmed.Content) != confirmed.SHA256 || sha(confirmed.EventsCSV) != confirmed.EventsCSVSHA256 {
		t.Errorf("confirm hashes do not match the content")
	}
	for name, text := range map[string]string{"json": confirmed.Content, "csv": confirmed.EventsCSV} {
		if strings.Contains(text, intSentinel) || strings.Contains(text, intFileName) {
			t.Errorf("%s export carries client data or the file name", name)
		}
	}
	if !json.Valid([]byte(confirmed.Content)) {
		t.Errorf("export is not JSON")
	}
	// A preview is confirmed once.
	if res = e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+preview.PreviewID+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("second confirm = %d", res.Code)
	}

	// An unconfirmed preview does not survive the erase of its case.
	res = e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	decodeBody(t, res, &preview)
	evs, _ := e.eventsOf(id, "")
	if len(evs) == 0 {
		t.Fatal("no events were stored")
	}
	seedLedger(t, e.ledger, "Audit creates stall while HTTP.sys queues", []string{evs[0].EvidenceID}, "")
	if res = e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	if res = e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+preview.PreviewID+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("confirm after erase = %d, want 404", res.Code)
	}
	list, err := e.ledger.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range list {
		for _, ref := range h.EvidenceIDs {
			if strings.HasPrefix(ref, "ev:"+id[:8]+":") {
				t.Errorf("ledger still references the erased case: %q", ref)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(e.root, id)); !os.IsNotExist(err) {
		t.Errorf("case folder still exists after erase: %v", err)
	}
	if res = e.get("/api/cases/" + id + "/events"); res.Code != http.StatusNotFound {
		t.Errorf("events after erase = %d", res.Code)
	}
	if res = e.json(http.MethodPost, "/api/cases/"+id+"/timeline", intTimelineBody); res.Code != http.StatusNotFound {
		t.Errorf("timeline after erase = %d", res.Code)
	}
}

func TestIntegrationNoFileOnDiskHoldsClientText(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Synthetic at rest")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	_ = filepath.Walk(e.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), intSentinel) || strings.Contains(string(data), "orders/42") {
			t.Errorf("%s holds client text in the clear", path)
		}
		return nil
	})
}

func TestIntegrationCaseRoutesNeedTheToken(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Synthetic token")
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/cases/" + id + "/timeline", intTimelineBody},
		{http.MethodPost, "/api/export/preview", `{"caseId":"` + id + `"}`},
		{http.MethodGet, "/api/cases/" + id + "/events", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", "wrong-token")
		res := httptest.NewRecorder()
		e.h.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong token = %d", c.method, c.path, res.Code)
		}
	}
}

func TestTimelineRefusals(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Synthetic refusals")
	for name, body := range map[string]string{
		"not json":        `{`,
		"bad threshold":   strings.Replace(intTimelineBody, `{"query"`, `{"auditStallMs":-5,"query"`, 1),
		"huge threshold":  strings.Replace(intTimelineBody, `{"query"`, `{"auditStallMs":1e12,"query"`, 1),
		"missing window":  `{"query":{"sourceIds":["src"]}}`,
		"unknown field x": strings.Replace(intTimelineBody, `{"query"`, `{"nope":1,"query"`, 1),
	} {
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/timeline", body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
		}
	}
	if res := e.json(http.MethodPost, "/api/cases/"+strings.Repeat("a", 26)+"/timeline", intTimelineBody); res.Code != http.StatusNotFound {
		t.Errorf("unknown case = %d", res.Code)
	}
	if res := e.json(http.MethodPost, "/api/cases/not-an-id/timeline", intTimelineBody); res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
		t.Errorf("bad id = %d", res.Code)
	}
}

func TestCaseExportRefusals(t *testing.T) {
	e := newIntEnv(t, intRepo())
	if res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+strings.Repeat("a", 26)+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("unknown case export = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"x/../y"}`); res.Code == http.StatusOK {
		t.Errorf("a path-like case id was accepted: %s", res.Body)
	}
}

func TestSpreadsheetSafeIsTheEvidenceCell(t *testing.T) {
	for _, in := range []string{"", "plain", "=1+1", "+x", "-x", "@x", "\tx", "'=x"} {
		if got, want := spreadsheetSafe(in), evidence.Cell(in); got != want {
			t.Errorf("spreadsheetSafe(%q) = %q, evidence.Cell = %q", in, got, want)
		}
	}
}

// A clumsy user confirms the same preview twice, confirms after erasing the
// case, or exports a case that holds nothing. None of these may panic or leak.
func TestCaseExportClumsyUse(t *testing.T) {
	e := newIntEnv(t, intRepo())
	confirm := func(previewID string) int {
		return e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+previewID+`"}`).Code
	}
	preview := func(id string) string {
		res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("preview of %s = %d %s", id, res.Code, res.Body)
		}
		var out struct {
			PreviewID string `json:"previewId"`
		}
		decodeBody(t, res, &out)
		return out.PreviewID
	}

	empty := e.newCase("Nothing imported")
	pid := preview(empty)
	if got := confirm(pid); got != http.StatusOK {
		t.Errorf("confirm of an empty case export = %d", got)
	}
	if got := confirm(pid); got != http.StatusNotFound {
		t.Errorf("second confirm of the same preview = %d, want 404", got)
	}

	for _, bad := range []string{"", "x", strings.Repeat("z", 5000)} {
		if got := confirm(bad); got != http.StatusNotFound {
			t.Errorf("confirm of %d-byte token = %d, want 404", len(bad), got)
		}
	}

	gone := e.newCase("Erased before confirm")
	e.commit(gone, "iis-httperr", "web-a", intHTTPERR)
	pid = preview(gone)
	if res := e.json(http.MethodDelete, "/api/cases/"+gone, `{"confirm":"`+gone+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	if got := confirm(pid); got != http.StatusNotFound {
		t.Errorf("confirm after the case was erased = %d, want 404", got)
	}
	if res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+gone+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("preview of an erased case = %d, want 404", res.Code)
	}
	if res := e.json(http.MethodDelete, "/api/cases/"+gone, `{"confirm":"`+gone+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("second erase = %d, want 404", res.Code)
	}
}
