package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/hosts"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/ledger"
)

// secretCell stands for client data inside a file. No response may carry it.
const secretCell = "zz-cell-sentinel-91"

const toyHeader = "time,host,note,ms"

// toyImporter reads "time,host,note,ms" rows. It is a stand-in for the real
// importers, which these tests never touch.
type toyImporter struct {
	name    string
	score   int
	onParse func()
}

func (t toyImporter) Name() string { return t.name }

func (t toyImporter) Detect(head []byte, _ string) int {
	if bytes.HasPrefix(head, []byte(toyHeader)) {
		return t.score
	}
	return 0
}

func (t toyImporter) AllowedAttrs() map[string]bool { return map[string]bool{"note": true} }

func (t toyImporter) DefaultLimits() importer.Limits {
	l := importer.NewLimits(4096, 50)
	l.MaxRejectFraction = 0.5
	return l
}

func (t toyImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	if t.onParse != nil {
		t.onParse()
	}
	rep := importer.Report{Importer: t.name}
	sc := bufio.NewScanner(r)
	var line int64
	for sc.Scan() {
		line++
		text := sc.Text()
		if line == 1 {
			if text != toyHeader {
				return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "time"}
			}
			continue
		}
		rep.Rows++
		f := strings.Split(text, ",")
		if len(f) != 4 {
			rep.Reject(line, importer.ReasonFieldCount)
			continue
		}
		ts, err := time.Parse(time.RFC3339, f[0])
		if err != nil {
			rep.Reject(line, importer.ReasonBadTime)
			continue
		}
		if f[2] == "BOOM" {
			return rep, fmt.Errorf("bad cell %s", secretCell)
		}
		label := ""
		switch {
		case opts.Host != nil:
			label = opts.Host.Label
		case opts.Hosts != nil:
			hint, ok := opts.Hosts(f[1])
			if !ok {
				return rep, &importer.QuarantineError{Reason: importer.ReasonUnknownHost, Column: "host"}
			}
			label = hint.Label
		default:
			return rep, &importer.QuarantineError{Reason: importer.ReasonUnknownHost, Column: "host"}
		}
		ms, err := strconv.ParseFloat(f[3], 64)
		if err != nil {
			rep.Reject(line, importer.ReasonBadNumber)
			continue
		}
		kind := "toy"
		if ms >= 100 {
			kind = "toy-slow"
		}
		ev := evidence.Event{
			EvidenceID: evidence.EventID(opts.CaseID, t.name, opts.ImportNonce, line, 0),
			Source:     t.name, Kind: kind, Host: label, Start: ts.UTC(), End: ts.UTC(), DurationMs: &ms,
			Unit: "ms", Line: line, TZRule: clock.RuleUTCField, Attrs: map[string]string{"note": f[2]},
		}
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
		rep.Cover(ts)
	}
	if err := sc.Err(); err != nil {
		return rep, err
	}
	return rep, nil
}

const hostsDoc = `{"version":1,"hosts":[{"label":"web-a","aliases":["web-a.example.test"],"role":"iis","tz":"UTC"}]}`

type caseEnv struct {
	t      *testing.T
	h      http.Handler
	store  casestore.Store
	ledger ledger.Store
	saved  []*hosts.Map
}

func mustHosts(t *testing.T, doc string) *hosts.Map {
	t.Helper()
	m, err := hosts.Load(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func withMaxUpload(n int64) Option { return func(h *handler) { h.maxUpload = n } }

type envConfig struct {
	importers []importer.Importer
	options   []Option
	noSaver   bool
	saveErr   error
}

func newCaseEnv(t *testing.T, cfg envConfig) *caseEnv {
	t.Helper()
	if cfg.importers == nil {
		cfg.importers = []importer.Importer{toyImporter{name: "toy", score: 90}}
	}
	led, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	e := &caseEnv{t: t, store: casestore.NewMemory(), ledger: led}
	options := []Option{
		WithCases(e.store, mustHosts(t, hostsDoc), importer.NewRegistry(cfg.importers...)),
		WithLedger(led),
	}
	if !cfg.noSaver {
		options = append(options, WithHostsSaver(func(m *hosts.Map) error {
			if cfg.saveErr != nil {
				return cfg.saveErr
			}
			e.saved = append(e.saved, m)
			return nil
		}))
	}
	options = append(options, cfg.options...)
	e.h = New(&fakeRepository{}, testAssets(), testToken, false, options...)
	return e
}

func (e *caseEnv) serve(req *http.Request) *httptest.ResponseRecorder {
	req.Host = "127.0.0.1:8765"
	res := httptest.NewRecorder()
	e.h.ServeHTTP(res, req)
	return res
}

func (e *caseEnv) json(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Audit-Token", testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return e.serve(req)
}

func (e *caseEnv) get(path string) *httptest.ResponseRecorder {
	return e.json(http.MethodGet, path, "")
}

type formField struct{ name, value string }

type upload struct {
	name    string
	content string
}

func uploadBody(t *testing.T, fields []formField, file *upload) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		part, err := mw.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func (e *caseEnv) uploadRequest(path string, fields []formField, file *upload) *http.Request {
	body, contentType := uploadBody(e.t, fields, file)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", contentType)
	return req
}

var ackFields = []formField{{"ackGating", "true"}, {"ackText", "Gating acknowledged: the file is treated as read-only evidence."}}

func (e *caseEnv) upload(path string, fields []formField, file *upload) *httptest.ResponseRecorder {
	return e.serve(e.uploadRequest(path, fields, file))
}

func (e *caseEnv) newCase(title string) string {
	e.t.Helper()
	res := e.json(http.MethodPost, "/api/cases", `{"title":`+strconv.Quote(title)+`}`)
	if res.Code != http.StatusCreated {
		e.t.Fatalf("create case = %d %s", res.Code, res.Body)
	}
	var out caseDTO
	decodeBody(e.t, res, &out)
	return out.ID
}

func decodeBody(t *testing.T, res *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), into); err != nil {
		t.Fatalf("body is not the expected JSON (%d): %v", res.Code, err)
	}
}

func failureOf(t *testing.T, res *httptest.ResponseRecorder) caseFailure {
	t.Helper()
	var f caseFailure
	decodeBody(t, res, &f)
	return f
}

func toyCSV(rows ...string) string {
	return toyHeader + "\n" + strings.Join(rows, "\n") + "\n"
}

func toyRow(sec int, host, note string, ms int) string {
	return fmt.Sprintf("2001-01-01T00:00:%02dZ,%s,%s,%d", sec, host, note, ms)
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (e *caseEnv) importsOf(id string) []importRecordDTO {
	e.t.Helper()
	res := e.get("/api/cases/" + id + "/imports")
	if res.Code != http.StatusOK {
		e.t.Fatalf("list imports = %d %s", res.Code, res.Body)
	}
	var out struct {
		Imports []importRecordDTO `json:"imports"`
	}
	decodeBody(e.t, res, &out)
	return out.Imports
}

func (e *caseEnv) eventsOf(id, query string) (events []eventDTO, truncated bool) {
	e.t.Helper()
	res := e.get("/api/cases/" + id + "/events" + query)
	if res.Code != http.StatusOK {
		e.t.Fatalf("events = %d %s", res.Code, res.Body)
	}
	var out struct {
		Events    []eventDTO `json:"events"`
		Truncated bool       `json:"truncated"`
	}
	decodeBody(e.t, res, &out)
	return out.Events, out.Truncated
}

func commitPath(id string) string {
	return "/api/cases/" + id + "/imports?importer=toy&host=web-a"
}

/* ------------------------------------------------------------ lifecycle */

func TestCaseRoutesUnavailableWithoutStore(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/cases"},
		{http.MethodGet, "/api/hosts"},
		{http.MethodGet, "/api/cases/" + strings.Repeat("a", 26) + "/imports"},
	} {
		res := request(h, c.method, c.path, "", true)
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", c.method, c.path, res.Code)
		}
	}
}

func TestCaseRoutesRejectWrongToken(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Token case")
	body, contentType := uploadBody(t, ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	raw := body.Bytes()
	for _, token := range []string{"", "wrong-token", testToken + "x"} {
		for _, c := range []struct{ method, path, ct string }{
			{http.MethodGet, "/api/cases", ""},
			{http.MethodPost, "/api/cases", "application/json"},
			{http.MethodPost, "/api/cases/" + id + "/close", ""},
			{http.MethodDelete, "/api/cases/" + id, "application/json"},
			{http.MethodGet, "/api/cases/" + id + "/imports", ""},
			{http.MethodPost, commitPath(id), contentType},
			{http.MethodGet, "/api/cases/" + id + "/events", ""},
			{http.MethodGet, "/api/hosts", ""},
			{http.MethodPut, "/api/hosts", "application/json"},
		} {
			payload := []byte(`{"title":"x"}`)
			if strings.Contains(c.path, "/imports") && c.method == http.MethodPost {
				payload = raw
			}
			req := httptest.NewRequest(c.method, c.path, bytes.NewReader(payload))
			if c.ct != "" {
				req.Header.Set("Content-Type", c.ct)
			}
			if token != "" {
				req.Header.Set("X-Audit-Token", token)
			}
			if res := e.serve(req); res.Code != http.StatusUnauthorized {
				t.Errorf("%s %s token %q = %d, want 401", c.method, c.path, token, res.Code)
			}
		}
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a refused request stored %d imports", n)
	}
}

func TestCaseLifecycle(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	res := e.json(http.MethodPost, "/api/cases", `{"title":"  Sales cut-over  "}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var raw map[string]any
	decodeBody(t, res, &raw)
	for _, key := range []string{"id", "title", "created", "closed", "bytes", "imports"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("case JSON has no %q key: %v", key, raw)
		}
	}
	if raw["title"] != "Sales cut-over" || raw["closed"] != nil || raw["imports"].(float64) != 0 {
		t.Errorf("created case = %v", raw)
	}
	id := raw["id"].(string)
	if _, err := casestore.ParseID(id); err != nil {
		t.Fatalf("case id %q is not valid", id)
	}

	var list struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(t, e.get("/api/cases"), &list)
	if len(list.Cases) != 1 || list.Cases[0].ID != id {
		t.Fatalf("list = %+v", list.Cases)
	}

	res = e.json(http.MethodPost, "/api/cases/"+id+"/close", "")
	if res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	var closed caseDTO
	decodeBody(t, res, &closed)
	if closed.Closed == nil || closed.ID != id {
		t.Fatalf("closed case = %+v", closed)
	}

	// A wrong or partial confirmation erases nothing.
	for _, confirm := range []string{"", id[:8], strings.ToUpper(id), "yes"} {
		res = e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+strconv.Quote(confirm)+`}`)
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "confirm-mismatch" {
			t.Errorf("erase with confirm %q = %d %s", confirm, res.Code, res.Body)
		}
	}
	if res = e.json(http.MethodDelete, "/api/cases/"+id, ""); res.Code != http.StatusBadRequest && res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("erase without a body = %d, want 400 or 415", res.Code)
	}
	decodeBody(t, e.get("/api/cases"), &list)
	if len(list.Cases) != 1 {
		t.Fatal("a refused erase removed the case")
	}

	res = e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`)
	if res.Code != http.StatusNoContent || res.Body.Len() != 0 {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	decodeBody(t, e.get("/api/cases"), &list)
	if len(list.Cases) != 0 {
		t.Fatalf("case still listed after erase: %+v", list.Cases)
	}
	if res = e.get("/api/cases/" + id + "/imports"); res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("erased case imports = %d %s", res.Code, res.Body)
	}
	// Erase is idempotent, so a retry after a half-finished erase completes it.
	if res = e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Errorf("second erase = %d", res.Code)
	}
}

func TestCreateCaseRejectsBadTitles(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	for name, body := range map[string]string{
		"empty":        `{"title":""}`,
		"blank":        `{"title":"   "}`,
		"too long":     `{"title":"` + strings.Repeat("x", casestore.MaxTitleBytes+1) + `"}`,
		"control char": `{"title":"a\u0007b"}`,
	} {
		res := e.json(http.MethodPost, "/api/cases", body)
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-title" {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	for name, body := range map[string]string{
		"unknown field": `{"title":"ok","extra":1}`,
		"not json":      `title`,
	} {
		if res := e.json(http.MethodPost, "/api/cases", body); res.Code != http.StatusBadRequest && res.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d", name, res.Code)
		}
	}
	var list struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(t, e.get("/api/cases"), &list)
	if len(list.Cases) != 0 {
		t.Fatalf("a refused title created %d cases", len(list.Cases))
	}
}

func TestBadCaseIDIsRefusedFirst(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	body, contentType := uploadBody(t, ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	raw := body.Bytes()
	for _, id := range []string{
		"BAD", "short", strings.Repeat("a", 25), strings.Repeat("a", 27), strings.Repeat("A", 26),
		strings.Repeat("1", 26), strings.Repeat("a", 25) + "-", "..", "%2e%2e", strings.Repeat("a", 25) + "%00",
	} {
		for _, c := range []struct{ method, suffix string }{
			{http.MethodPost, "/close"}, {http.MethodDelete, ""}, {http.MethodGet, "/imports"},
			{http.MethodPost, "/imports?importer=toy"}, {http.MethodGet, "/events"},
		} {
			req := httptest.NewRequest(c.method, "/api/cases/"+id+c.suffix, bytes.NewReader(raw))
			req.Header.Set("X-Audit-Token", testToken)
			req.Header.Set("Content-Type", contentType)
			if c.method == http.MethodDelete {
				req = httptest.NewRequest(c.method, "/api/cases/"+id, strings.NewReader(`{"confirm":"`+id+`"}`))
				req.Header.Set("X-Audit-Token", testToken)
				req.Header.Set("Content-Type", "application/json")
			}
			res := e.serve(req)
			if res.Code != http.StatusBadRequest {
				t.Errorf("%s /api/cases/%s%s = %d %s, want 400", c.method, id, c.suffix, res.Code, res.Body)
				continue
			}
			if got := failureOf(t, res).Reason; got != "bad-case-id" {
				t.Errorf("%s /api/cases/%s%s reason = %q", c.method, id, c.suffix, got)
			}
		}
	}
	// A path separator in the id, even encoded, is not a route.
	for _, path := range []string{"/api/cases/..%2Fx/imports", "/api/cases/a%2Fb", "/api/cases/a%5Cb/imports"} {
		res := e.get(path)
		if res.Code != http.StatusNotFound && res.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 404 or 400", path, res.Code)
		}
	}
}

func TestUnknownCaseIs404(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := strings.Repeat("a", 26)
	body, contentType := uploadBody(t, ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	req := httptest.NewRequest(http.MethodPost, commitPath(id), body)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", contentType)
	for name, res := range map[string]*httptest.ResponseRecorder{
		"imports": e.get("/api/cases/" + id + "/imports"),
		"events":  e.get("/api/cases/" + id + "/events"),
		"close":   e.json(http.MethodPost, "/api/cases/"+id+"/close", ""),
		"import":  e.serve(req),
	} {
		if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
}

/* ------------------------------------------------------------ imports */

func TestCommitImportAndRead(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Import case")
	content := toyCSV(
		toyRow(1, "web-a.example.test", "alpha", 10),
		toyRow(2, "web-a.example.test", "beta", 150),
		toyRow(3, "web-a.example.test", "gamma", 20),
		"not-a-row",
	)
	res := e.upload(commitPath(id), ackFields, &upload{"export-2001.csv", content})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	var out struct {
		Importer string           `json:"importer"`
		Score    int              `json:"score"`
		Tied     []string         `json:"tied"`
		Report   *reportDTO       `json:"report"`
		Record   *importRecordDTO `json:"record"`
	}
	decodeBody(t, res, &out)
	if out.Importer != "toy" || out.Report == nil || out.Record == nil || len(out.Tied) != 0 {
		t.Fatalf("answer = %s", res.Body)
	}
	if out.Report.Rows != 4 || out.Report.Emitted != 3 || out.Report.Rejected != 1 ||
		len(out.Report.Errors) != 1 || out.Report.Errors[0].Line != 5 || out.Report.Errors[0].Reason != "field-count" {
		t.Errorf("report = %+v", out.Report)
	}
	if out.Report.First == nil || out.Report.Last == nil || out.Report.KnownGaps == nil || out.Report.ObservedHosts == nil {
		t.Errorf("report has nil lists or times: %+v", out.Report)
	}
	rec := out.Record
	if rec.ID < 1 || rec.Importer != "toy" || rec.FileName != "export-2001.csv" || rec.SHA256 != sha(content) ||
		rec.Bytes != int64(len(content)) || rec.Rows != 4 || rec.Rejected != 1 || rec.HostLabel != "web-a" ||
		rec.Status != casestore.StatusOK || rec.Reason != "" || rec.KnownGaps == nil {
		t.Errorf("record = %+v", rec)
	}
	if got := e.importsOf(id); len(got) != 1 || got[0].ID != rec.ID || got[0].SHA256 != rec.SHA256 {
		t.Errorf("imports = %+v", got)
	}
	var info struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(t, e.get("/api/cases"), &info)
	if len(info.Cases) != 1 || info.Cases[0].Imports != 1 || info.Cases[0].Bytes < int64(len(content)) {
		t.Errorf("case info = %+v", info.Cases)
	}

	events, truncated := e.eventsOf(id, "")
	if len(events) != 3 || truncated {
		t.Fatalf("events = %d truncated=%v", len(events), truncated)
	}
	first := events[0]
	if first.Source != "toy" || first.Kind != "toy" || first.Host != "web-a" || first.Line != 2 || first.TZRule != clock.RuleUTCField ||
		first.Attrs["note"] != "alpha" || first.DurationMs == nil || *first.DurationMs != 10 ||
		!first.Start.Equal(time.Date(2001, 1, 1, 0, 0, 1, 0, time.UTC)) {
		t.Errorf("first event = %+v", first)
	}
	if !strings.HasPrefix(first.EvidenceID, "ev:"+id[:8]+":toy:") {
		t.Errorf("evidence id = %q", first.EvidenceID)
	}
	// camelCase keys, as the UI contract says.
	var rawEvents struct {
		Events []map[string]any `json:"events"`
	}
	decodeBody(t, e.get("/api/cases/"+id+"/events"), &rawEvents)
	for _, key := range []string{"evidenceId", "durationMs", "tzRule", "start", "end", "attrs"} {
		if _, ok := rawEvents.Events[0][key]; !ok {
			t.Errorf("event JSON has no %q key", key)
		}
	}
}

func TestCommitWithoutHostParamUsesHostMap(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Host map case")
	res := e.upload("/api/cases/"+id+"/imports?importer=toy", ackFields, &upload{"a.csv", toyCSV(toyRow(1, "WEB-A.example.test.", "alpha", 5))})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	if events, _ := e.eventsOf(id, ""); len(events) != 1 || events[0].Host != "web-a" {
		t.Fatalf("events = %+v", events)
	}
}

func TestCommitNeedsAcknowledgementBeforeTheFile(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Ack case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
	for name, fields := range map[string][]formField{
		"no fields":      nil,
		"gating only":    {{"ackGating", "true"}},
		"text only":      {{"ackText", "Acknowledged"}},
		"gating false":   {{"ackGating", "false"}, {"ackText", "Acknowledged"}},
		"gating garbage": {{"ackGating", "yes"}, {"ackText", "Acknowledged"}},
		"empty text":     {{"ackGating", "true"}, {"ackText", ""}},
	} {
		res := e.upload(commitPath(id), fields, file)
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "ack-required" {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	// The fields travel before the file; after it they are too late.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", file.name)
	io.WriteString(part, file.content)
	for _, f := range ackFields {
		mw.WriteField(f.name, f.value)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, commitPath(id), &buf)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if res := e.serve(req); res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "ack-required" {
		t.Errorf("ack after file: %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a refused commit stored %d imports", n)
	}
}

func TestImportRequestShapeErrors(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Shape case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
	check := func(name string, res *httptest.ResponseRecorder, status int, reason string) {
		t.Helper()
		if res.Code != status || failureOf(t, res).Reason != reason {
			t.Errorf("%s: %d %s, want %d %s", name, res.Code, res.Body, status, reason)
		}
	}
	check("no importer on commit", e.upload("/api/cases/"+id+"/imports", ackFields, file), 400, "importer-required")
	check("unknown importer", e.upload("/api/cases/"+id+"/imports?importer=nope", ackFields, file), 400, "unknown-importer")
	check("importer with a path", e.upload("/api/cases/"+id+"/imports?importer=..%2Ftoy", ackFields, file), 400, "unknown-importer")
	check("no file", e.upload(commitPath(id), ackFields, nil), 400, "file-missing")
	check("unknown field", e.upload(commitPath(id), append([]formField{{"extra", "1"}}, ackFields...), file), 400, "bad-form")
	req := httptest.NewRequest(http.MethodPost, commitPath(id), strings.NewReader(`{"title":"x"}`))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	check("not multipart", e.serve(req), 415, "not-multipart")
	req = httptest.NewRequest(http.MethodPost, commitPath(id), strings.NewReader("--x\r\nbroken"))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if res := e.serve(req); res.Code != http.StatusBadRequest {
		t.Errorf("broken multipart = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("refused requests stored %d imports", n)
	}
}

func TestImportIntoClosedCaseIsRefused(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Closed case")
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
		t.Fatalf("closed case import = %d %s", res.Code, res.Body)
	}
	res = e.upload(commitPath(id)+"&preview=1", nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusConflict {
		t.Fatalf("closed case preview = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("closed case holds %d imports", n)
	}
}

func TestDuplicateImportIsRefusedWithTheExistingID(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Duplicate case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
	res := e.upload(commitPath(id), ackFields, file)
	if res.Code != http.StatusCreated {
		t.Fatal(res.Body)
	}
	var first struct {
		Record importRecordDTO `json:"record"`
	}
	decodeBody(t, res, &first)
	// Same bytes under another name and another host choice are still the same bytes.
	res = e.upload("/api/cases/"+id+"/imports?importer=toy", ackFields, &upload{"renamed.csv", file.content})
	f := failureOf(t, res)
	if res.Code != http.StatusConflict || f.Reason != "duplicate" || f.ImportID != first.Record.ID {
		t.Fatalf("duplicate = %d %s", res.Code, res.Body)
	}
	// A preview of the same bytes says so too, before anything is committed.
	res = e.upload(commitPath(id)+"&preview=1", nil, file)
	f = failureOf(t, res)
	if res.Code != http.StatusConflict || f.Reason != "duplicate" || f.ImportID != first.Record.ID {
		t.Fatalf("duplicate preview = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 1 {
		t.Fatalf("imports = %d, want 1", n)
	}
}

func TestPreviewStoresNothing(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Preview case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5), toyRow(2, "web-a", "beta", 500))}
	res := e.upload(commitPath(id)+"&preview=1", nil, file)
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var out struct {
		Importer string           `json:"importer"`
		Report   *reportDTO       `json:"report"`
		Record   *importRecordDTO `json:"record"`
	}
	decodeBody(t, res, &out)
	if out.Importer != "toy" || out.Report == nil || out.Report.Emitted != 2 || out.Record != nil {
		t.Fatalf("preview answer = %s", res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("preview stored %d imports", n)
	}
	if events, _ := e.eventsOf(id, ""); len(events) != 0 {
		t.Fatalf("preview stored %d events", len(events))
	}
	var list struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(t, e.get("/api/cases"), &list)
	if list.Cases[0].Imports != 0 || list.Cases[0].Bytes != 0 {
		t.Fatalf("preview changed the case: %+v", list.Cases[0])
	}
	// Preview needs no acknowledgement, and the same bytes then commit normally.
	if res = e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated {
		t.Fatalf("commit after preview = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 1 {
		t.Fatalf("imports = %d, want 1", n)
	}
}

func TestPreviewDetectsTheImporter(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Detect case")
	path := "/api/cases/" + id + "/imports?preview=1&host=web-a"
	res := e.upload(path, nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusOK {
		t.Fatalf("detect = %d %s", res.Code, res.Body)
	}
	var out struct {
		Importer string     `json:"importer"`
		Score    int        `json:"score"`
		Tied     []string   `json:"tied"`
		Report   *reportDTO `json:"report"`
	}
	decodeBody(t, res, &out)
	if out.Importer != "toy" || out.Score != 90 || len(out.Tied) != 0 || out.Report == nil || out.Report.Emitted != 1 {
		t.Fatalf("detect answer = %s", res.Body)
	}

	// Nothing recognises the file.
	res = e.upload(path, nil, &upload{"a.txt", "hello\nworld\n"})
	if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "unsupported-format" {
		t.Fatalf("unrecognised = %d %s", res.Code, res.Body)
	}
	// An archive is refused before detection.
	res = e.upload(path, nil, &upload{"a.gz", "\x1f\x8b\x08\x00\x00\x00\x00\x00" + strings.Repeat("x", 64)})
	if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != string(importer.ReasonArchive) {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("detection stored %d imports", n)
	}
}

func TestPreviewTieReturnsCandidatesAndNoReport(t *testing.T) {
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{
		toyImporter{name: "toy", score: 90}, toyImporter{name: "toy-twin", score: 85},
	}})
	id := e.newCase("Tie case")
	res := e.upload("/api/cases/"+id+"/imports?preview=1&host=web-a", nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusOK {
		t.Fatalf("tie = %d %s", res.Code, res.Body)
	}
	var out struct {
		Tied   []string   `json:"tied"`
		Report *reportDTO `json:"report"`
		Record any        `json:"record"`
	}
	decodeBody(t, res, &out)
	if len(out.Tied) != 2 || out.Report != nil || out.Record != nil {
		t.Fatalf("tie answer = %s", res.Body)
	}
	// Naming the importer resolves the tie.
	res = e.upload("/api/cases/"+id+"/imports?preview=1&host=web-a&importer=toy-twin", nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusOK {
		t.Fatalf("named = %d %s", res.Code, res.Body)
	}
}

func TestFileNamesAreReducedToBaseNames(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Name case")
	for i, c := range []struct{ sent, want string }{
		{"../../etc/passwd", "passwd"},
		{`..\..\Windows\system32\x.csv`, "x.csv"},
		{"/abs/path/log.csv", "log.csv"},
		{`C:\Users\someone\log.csv`, "log.csv"},
		{"....//..", "upload"},
		{"plain.csv", "plain.csv"},
	} {
		// Each import needs distinct bytes, or the second is a duplicate.
		res := e.upload(commitPath(id), ackFields, &upload{c.sent, toyCSV(toyRow(i+1, "web-a", "alpha", 5))})
		if res.Code != http.StatusCreated {
			t.Fatalf("%q: %d %s", c.sent, res.Code, res.Body)
		}
		var out struct {
			Record importRecordDTO `json:"record"`
		}
		decodeBody(t, res, &out)
		if out.Record.FileName != c.want {
			t.Errorf("%q stored as %q, want %q", c.sent, out.Record.FileName, c.want)
		}
	}
}

func TestBaseName(t *testing.T) {
	long := strings.Repeat("é", 200)
	for _, c := range []struct{ in, want string }{
		{"a.csv", "a.csv"},
		{"dir/a.csv", "a.csv"},
		{`dir\a.csv`, "a.csv"},
		{"a/b/c/d.txt", "d.txt"},
		{"..", "upload"},
		{"", "upload"},
		{"   ", "upload"},
		{"...", "upload"},
		{"a..b.csv", "ab.csv"},
		{"a....b", "ab"},
		{"x\x00y.csv", "xy.csv"},
		{"x\r\ny\t.csv", "xy.csv"},
		{"x\u202ey.csv", "xy.csv"},
		{`bad:*?"<>|name.csv`, "badname.csv"},
		{"trailing. ", "trailing"},
		{"\xff\xfe.csv", "csv"},
		{"dir/", "upload"},
	} {
		if got := baseName(c.in); got != c.want {
			t.Errorf("baseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := baseName(long)
	if len(got) > maxFileNameBytes || !strings.HasPrefix(long, got) {
		t.Errorf("long name is %d bytes", len(got))
	}
	for _, in := range []string{"a/../b", "..\\..\\x", "%2e%2e/x", "a\x00../b"} {
		out := baseName(in)
		if strings.ContainsAny(out, `/\`) || strings.Contains(out, "..") {
			t.Errorf("baseName(%q) = %q still has a separator or ..", in, out)
		}
	}
}

/* ------------------------------------------------------------ limits */

func TestImportOverTheRequestCapIsRefused(t *testing.T) {
	e := newCaseEnv(t, envConfig{options: []Option{withMaxUpload(2048)}})
	id := e.newCase("Cap case")
	big := toyCSV(toyRow(1, "web-a", secretCell, 5))
	big += strings.Repeat("x", 6000) + "\n"

	// A declared length over the cap is refused before anything is read.
	res := e.upload(commitPath(id), ackFields, &upload{"big.csv", big})
	if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "request-too-large" {
		t.Fatalf("declared = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), secretCell) {
		t.Fatal("the refusal carries file content")
	}

	// A body of unknown length is cut off by the reader at the cap.
	req := e.uploadRequest(commitPath(id), ackFields, &upload{"big.csv", big})
	req.ContentLength = -1
	res = e.serve(req)
	if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "request-too-large" {
		t.Fatalf("streamed = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), secretCell) {
		t.Fatal("the refusal carries file content")
	}

	// The same goes for a preview.
	req = e.uploadRequest(commitPath(id)+"&preview=1", nil, &upload{"big.csv", big})
	req.ContentLength = -1
	if res = e.serve(req); res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed preview = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a refused upload stored %d imports", n)
	}
	if events, _ := e.eventsOf(id, ""); len(events) != 0 {
		t.Fatalf("a refused upload stored %d events", len(events))
	}
}

func TestImporterLimitsAreRefusedWithAWord(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Limit case")

	// Over the importer's own byte limit (4096 for the toy importer).
	var rows []string
	for i := 0; i < 40; i++ {
		rows = append(rows, toyRow(i%60, "web-a", secretCell, 5))
	}
	over := toyCSV(rows...) + strings.Repeat("y", 5000) + "\n"
	res := e.upload(commitPath(id), ackFields, &upload{"over.csv", over})
	if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "limit-bytes" {
		t.Fatalf("bytes = %d %s", res.Code, res.Body)
	}

	// Over the row limit (50): small enough in bytes.
	rows = rows[:0]
	for i := 0; i < 60; i++ {
		rows = append(rows, toyRow(i%60, "web-a", "r", 5))
	}
	res = e.upload(commitPath(id), ackFields, &upload{"rows.csv", toyCSV(rows...)})
	if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "limit-rows" {
		t.Fatalf("rows = %d %s", res.Code, res.Body)
	}

	// Most rows rejected: quarantined with a reason, nothing else.
	rows = rows[:0]
	for i := 0; i < 10; i++ {
		rows = append(rows, "broken,"+secretCell)
	}
	res = e.upload(commitPath(id), ackFields, &upload{"rejects.csv", toyCSV(rows...)})
	f := failureOf(t, res)
	if res.Code != http.StatusUnprocessableEntity || f.Reason != string(importer.ReasonRejectFraction) {
		t.Fatalf("rejects = %d %s", res.Code, res.Body)
	}
	for _, body := range []string{res.Body.String()} {
		if strings.Contains(body, secretCell) {
			t.Fatal("a refusal carries file content")
		}
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("refused files stored %d imports", n)
	}
	if events, _ := e.eventsOf(id, ""); len(events) != 0 {
		t.Fatalf("refused files stored %d events", len(events))
	}
}

func TestImporterErrorsNeverReachTheResponse(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Error case")
	res := e.upload(commitPath(id), ackFields, &upload{"boom.csv", toyCSV(toyRow(1, "web-a", "BOOM", 5))})
	f := failureOf(t, res)
	if res.Code != http.StatusInternalServerError || f.Reason != "import-failed" {
		t.Fatalf("importer error = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), secretCell) || strings.Contains(res.Body.String(), "bad cell") {
		t.Fatalf("the importer's error text reached the response: %s", res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a failed import stored %d imports", n)
	}
}

func TestUnknownHostIsRefused(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Host case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "db-z.example.test", secretCell, 5))}

	// The picked host is not in the map.
	for _, path := range []string{
		"/api/cases/" + id + "/imports?importer=toy&host=nope",
		"/api/cases/" + id + "/imports?importer=toy&host=nope&preview=1",
	} {
		res := e.upload(path, ackFields, file)
		if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "unknown-host" {
			t.Errorf("%s = %d %s", path, res.Code, res.Body)
		}
	}
	// A name inside the file is not in the map.
	res := e.upload("/api/cases/"+id+"/imports?importer=toy", ackFields, file)
	f := failureOf(t, res)
	if res.Code != http.StatusUnprocessableEntity || f.Reason != "unknown-host" || f.Column != "host" {
		t.Errorf("host in file = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "db-z") || strings.Contains(res.Body.String(), secretCell) {
		t.Errorf("the refusal names the host or content: %s", res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("refused imports stored %d records", n)
	}
}

func TestConcurrentImportsToOneCaseRunOneAtATime(t *testing.T) {
	var inflight, peak, started atomic.Int32
	slow := toyImporter{name: "toy", score: 90, onParse: func() {
		started.Add(1)
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		inflight.Add(-1)
	}}
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{slow}})
	id := e.newCase("Concurrent case")
	other := e.newCase("Other case")

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := e.upload(commitPath(id), ackFields, &upload{"c.csv", toyCSV(toyRow(i+1, "web-a", "alpha", 5))})
			codes[i] = res.Code
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusCreated {
			t.Errorf("import %d = %d", i, code)
		}
	}
	if p := peak.Load(); p != 1 {
		t.Fatalf("%d imports ran at once in one case", p)
	}
	if got := len(e.importsOf(id)); got != n {
		t.Fatalf("imports = %d, want %d", got, n)
	}
	if got := len(e.importsOf(other)); got != 0 {
		t.Fatalf("another case holds %d imports", got)
	}
	events, _ := e.eventsOf(id, "")
	seen := map[string]bool{}
	for _, ev := range events {
		if seen[ev.EvidenceID] {
			t.Fatalf("evidence id %q repeats", ev.EvidenceID)
		}
		seen[ev.EvidenceID] = true
	}
	if len(events) != n {
		t.Fatalf("events = %d, want %d", len(events), n)
	}
}

func TestCaseGateSerialisesAndReleases(t *testing.T) {
	var g caseGates
	id := casestore.CaseID(strings.Repeat("a", 26))
	release, err := g.acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// A second caller waits, and gives up when its context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire = %v, want deadline exceeded", err)
	}
	// Another case is not blocked.
	other, err := g.acquire(context.Background(), casestore.CaseID(strings.Repeat("b", 26)))
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	again, err := g.acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	again()
	g.mu.Lock()
	left := len(g.m)
	g.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d gates leaked", left)
	}
}

/* ------------------------------------------------------------ events */

func TestEventFilters(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Events case")
	res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(
		toyRow(1, "web-a", "a", 10), toyRow(2, "web-a", "b", 200), toyRow(3, "web-a", "c", 10),
		toyRow(4, "web-a", "d", 300), toyRow(5, "web-a", "e", 10),
	)})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	at := func(sec int) string { return fmt.Sprintf("2001-01-01T00:00:%02dZ", sec) }
	for _, c := range []struct {
		name, query string
		want        []string
		truncated   bool
	}{
		{"all", "", []string{"a", "b", "c", "d", "e"}, false},
		{"from", "?from=" + at(3), []string{"c", "d", "e"}, false},
		{"to", "?to=" + at(2), []string{"a", "b"}, false},
		{"window", "?from=" + at(2) + "&to=" + at(4), []string{"b", "c", "d"}, false},
		{"kind", "?kind=toy-slow", []string{"b", "d"}, false},
		{"kinds comma", "?kind=toy-slow,toy", []string{"a", "b", "c", "d", "e"}, false},
		{"kinds repeated", "?kind=toy-slow&kind=toy", []string{"a", "b", "c", "d", "e"}, false},
		{"kind none", "?kind=nothing", nil, false},
		{"source", "?source=toy", []string{"a", "b", "c", "d", "e"}, false},
		{"source none", "?source=other", nil, false},
		{"host", "?host=web-a&kind=toy-slow", []string{"b", "d"}, false},
		{"host none", "?host=web-b", nil, false},
		{"limit", "?limit=2", []string{"a", "b"}, true},
		{"limit exact", "?limit=5", []string{"a", "b", "c", "d", "e"}, true},
		{"limit over", "?limit=6", []string{"a", "b", "c", "d", "e"}, false},
	} {
		events, truncated := e.eventsOf(id, c.query)
		var got []string
		for _, ev := range events {
			got = append(got, ev.Attrs["note"])
		}
		if strings.Join(got, "") != strings.Join(c.want, "") || truncated != c.truncated {
			t.Errorf("%s: got %v truncated=%v, want %v truncated=%v", c.name, got, truncated, c.want, c.truncated)
		}
	}
	for name, query := range map[string]string{
		"bad from":     "?from=yesterday",
		"bad to":       "?to=2001-01-01",
		"reversed":     "?from=" + at(4) + "&to=" + at(2),
		"limit zero":   "?limit=0",
		"limit text":   "?limit=many",
		"limit huge":   "?limit=" + strconv.Itoa(casestore.MaxEventQuery+1),
		"long kind":    "?kind=" + strings.Repeat("k", 65),
		"many kinds":   "?kind=" + strings.Repeat("k,", maxEventFilters+1),
		"no zone time": "?from=2001-01-01T00:00:01",
	} {
		if res := e.get("/api/cases/" + id + "/events" + query); res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, res.Code, res.Body)
		}
	}
	events, _ := e.eventsOf(id, "")
	if len(events) != 5 {
		t.Fatalf("events = %d", len(events))
	}
}

func TestEventsDefaultLimitIsTheStoreCap(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Limit case")
	var out struct {
		Limit int `json:"limit"`
	}
	decodeBody(t, e.get("/api/cases/"+id+"/events"), &out)
	if out.Limit != casestore.MaxEventQuery {
		t.Fatalf("limit = %d, want %d", out.Limit, casestore.MaxEventQuery)
	}
}

/* ------------------------------------------------------------ erase and ledger */

func seedLedger(t *testing.T, led ledger.Store, text string, ids []string, notes string) ledger.Hypothesis {
	t.Helper()
	h, err := led.Create(text)
	if err != nil {
		t.Fatal(err)
	}
	in := ledger.Input{
		Text: text, State: ledger.StateOpen, EvidenceIDs: ids, Notes: notes,
		Facts: ledger.Facts{Counterexamples: []string{}, Alternatives: []ledger.Alternative{{Text: "Scheduled index rebuild", Status: ledger.AltOpen}}},
	}
	out, err := led.Update(h.ID, in)
	if err != nil && notes != "" {
		// The notes check refuses long digit runs, and a random id can hold one. Seed without notes then.
		in.Notes = ""
		out, err = led.Update(h.ID, in)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEraseStripsTheLedgerOfThatCaseOnly(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	a := e.newCase("Case A")
	b := e.newCase("Case B")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
	for _, id := range []string{a, b} {
		// Different case ids give different evidence ids for the same bytes.
		if res := e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated {
			t.Fatalf("commit = %d %s", res.Code, res.Body)
		}
	}
	evA, _ := e.eventsOf(a, "")
	evB, _ := e.eventsOf(b, "")
	idA, idB := evA[0].EvidenceID, evB[0].EvidenceID
	seedLedger(t, e.ledger, "Audit-create stalls coincide with a lock wait", []string{idA, idB, "ep:2001-01-02t03"}, "first stall "+idA)
	seedLedger(t, e.ledger, "Only the other case", []string{idB}, "")

	res := e.json(http.MethodDelete, "/api/cases/"+a, `{"confirm":"`+a+`"}`)
	if res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	list, err := e.ledger.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range list {
		for _, id := range h.EvidenceIDs {
			if strings.HasPrefix(id, "ev:"+a[:8]+":") {
				t.Errorf("ledger still holds a reference to the erased case: %q", id)
			}
		}
		if strings.Contains(h.Notes, idA) {
			t.Errorf("ledger notes still hold %q", idA)
		}
	}
	var withB int
	for _, h := range list {
		for _, id := range h.EvidenceIDs {
			if id == idB {
				withB++
			}
		}
	}
	if withB != 2 {
		t.Errorf("other case references = %d, want 2", withB)
	}
	if events, _ := e.eventsOf(b, ""); len(events) != 1 {
		t.Errorf("the other case lost its events")
	}
	// The erased case's own events are gone with it.
	if res := e.get("/api/cases/" + a + "/events"); res.Code != http.StatusNotFound {
		t.Errorf("erased events = %d", res.Code)
	}
}

func TestEraseWorksWithoutALedger(t *testing.T) {
	store := casestore.NewMemory()
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithCases(store, nil, importer.NewRegistry()))
	res := request(h, http.MethodPost, "/api/cases", `{"title":"No ledger"}`, true)
	var created caseDTO
	decodeBody(t, res, &created)
	res = request(h, http.MethodDelete, "/api/cases/"+created.ID, `{"confirm":"`+created.ID+`"}`, true)
	if res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
}

func TestEraseWaitsForAnImportInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	blocking := toyImporter{name: "toy", score: 90, onParse: func() {
		once.Do(func() { close(entered) })
		<-release
	}}
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{blocking}})
	id := e.newCase("Erase case")

	importDone := make(chan int, 1)
	go func() {
		res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
		importDone <- res.Code
	}()
	<-entered
	eraseDone := make(chan int, 1)
	go func() {
		eraseDone <- e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`).Code
	}()
	select {
	case code := <-eraseDone:
		t.Fatalf("erase finished (%d) while an import was running", code)
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	if code := <-importDone; code != http.StatusCreated {
		t.Fatalf("import = %d", code)
	}
	if code := <-eraseDone; code != http.StatusNoContent {
		t.Fatalf("erase = %d", code)
	}
	if res := e.get("/api/cases/" + id + "/imports"); res.Code != http.StatusNotFound {
		t.Fatalf("erased case imports = %d", res.Code)
	}
}

/* ------------------------------------------------------------ hosts */

func TestGetHosts(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	res := e.get("/api/hosts")
	if res.Code != http.StatusOK {
		t.Fatalf("hosts = %d %s", res.Code, res.Body)
	}
	var doc struct {
		Version int `json:"version"`
		Hosts   []struct {
			Label   string   `json:"label"`
			Aliases []string `json:"aliases"`
			Role    string   `json:"role"`
			TZ      string   `json:"tz"`
		} `json:"hosts"`
	}
	decodeBody(t, res, &doc)
	if doc.Version != 1 || len(doc.Hosts) != 1 || doc.Hosts[0].Label != "web-a" || doc.Hosts[0].Role != "iis" ||
		len(doc.Hosts[0].Aliases) != 1 || doc.Hosts[0].Aliases[0] != "web-a.example.test" {
		t.Fatalf("hosts = %s", res.Body)
	}

	// No map at all is an empty list, not null.
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithCases(casestore.NewMemory(), nil, importer.NewRegistry()))
	res = request(h, http.MethodGet, "/api/hosts", "", true)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != `{"version":1,"hosts":[]}` {
		t.Fatalf("empty hosts = %d %s", res.Code, res.Body)
	}
}

func TestPutHostsReplacesTheMap(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Hosts case")
	next := `{"version":1,"hosts":[{"label":"web-b","aliases":["web-b.example.test"],"role":"iis","tz":"UTC"}]}`
	res := e.json(http.MethodPut, "/api/hosts", next)
	if res.Code != http.StatusOK {
		t.Fatalf("put = %d %s", res.Code, res.Body)
	}
	if len(e.saved) != 1 || e.saved[0].Len() != 1 || e.saved[0].Labels()[0] != "web-b" {
		t.Fatalf("saved = %+v", e.saved)
	}
	var doc struct {
		Hosts []struct {
			Label string `json:"label"`
		} `json:"hosts"`
	}
	decodeBody(t, e.get("/api/hosts"), &doc)
	if len(doc.Hosts) != 1 || doc.Hosts[0].Label != "web-b" {
		t.Fatalf("hosts after put = %+v", doc.Hosts)
	}
	// The new map is the one imports use: web-a is gone, web-b works.
	res = e.upload("/api/cases/"+id+"/imports?importer=toy", ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a.example.test", "alpha", 5))})
	if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "unknown-host" {
		t.Fatalf("old host = %d %s", res.Code, res.Body)
	}
	res = e.upload("/api/cases/"+id+"/imports?importer=toy", ackFields, &upload{"b.csv", toyCSV(toyRow(1, "web-b.example.test", "alpha", 5))})
	if res.Code != http.StatusCreated {
		t.Fatalf("new host = %d %s", res.Code, res.Body)
	}
}

func TestPutHostsRefusals(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	const sentinel = "zz-host-sentinel-17"
	for name, body := range map[string]string{
		"unknown field": `{"version":1,"hosts":[],"` + sentinel + `":1}`,
		"bad version":   `{"version":2,"hosts":[]}`,
		"empty label":   `{"version":1,"hosts":[{"label":""}]}`,
		"bad tz":        `{"version":1,"hosts":[{"label":"x","tz":"` + sentinel + `"}]}`,
		"bad role":      `{"version":1,"hosts":[{"label":"x","role":"` + sentinel + `"}]}`,
		"not json":      `[` + sentinel,
		"trailing data": `{"version":1,"hosts":[]} ` + sentinel,
	} {
		res := e.json(http.MethodPut, "/api/hosts", body)
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "invalid-hosts" {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), sentinel) {
			t.Errorf("%s: the refusal echoes the value: %s", name, res.Body)
		}
	}
	// Over the file cap.
	big := `{"version":1,"hosts":[{"label":"x","aliases":["` + strings.Repeat("a", hosts.MaxFileBytes) + `"]}]}`
	if res := e.json(http.MethodPut, "/api/hosts", big); res.Code != http.StatusRequestEntityTooLarge && res.Code != http.StatusBadRequest {
		t.Errorf("oversize = %d, want 413 or 400", res.Code)
	}
	// Wrong content type.
	req := httptest.NewRequest(http.MethodPut, "/api/hosts", strings.NewReader(hostsDoc))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "text/plain")
	if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d", res.Code)
	}
	if len(e.saved) != 0 {
		t.Fatalf("a refused document was saved: %d", len(e.saved))
	}
	var doc struct {
		Hosts []struct{ Label string } `json:"hosts"`
	}
	decodeBody(t, e.get("/api/hosts"), &doc)
	if len(doc.Hosts) != 1 || doc.Hosts[0].Label != "web-a" {
		t.Fatalf("refused documents changed the map: %+v", doc.Hosts)
	}
}

func TestPutHostsNeedsASaver(t *testing.T) {
	e := newCaseEnv(t, envConfig{noSaver: true})
	res := e.json(http.MethodPut, "/api/hosts", hostsDoc)
	if res.Code != http.StatusServiceUnavailable || failureOf(t, res).Reason != "hosts-readonly" {
		t.Fatalf("put without a saver = %d %s", res.Code, res.Body)
	}
	if res = e.get("/api/hosts"); res.Code != http.StatusOK {
		t.Fatalf("get = %d", res.Code)
	}
}

func TestPutHostsKeepsTheOldMapWhenSavingFails(t *testing.T) {
	e := newCaseEnv(t, envConfig{saveErr: errors.New("disk full at " + secretCell)})
	next := `{"version":1,"hosts":[{"label":"web-b"}]}`
	res := e.json(http.MethodPut, "/api/hosts", next)
	if res.Code != http.StatusInternalServerError || failureOf(t, res).Reason != "hosts-not-saved" {
		t.Fatalf("put = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), secretCell) {
		t.Fatalf("the save error text reached the response: %s", res.Body)
	}
	var doc struct {
		Hosts []struct{ Label string } `json:"hosts"`
	}
	decodeBody(t, e.get("/api/hosts"), &doc)
	if len(doc.Hosts) != 1 || doc.Hosts[0].Label != "web-a" {
		t.Fatalf("a failed save changed the map: %+v", doc.Hosts)
	}
}
