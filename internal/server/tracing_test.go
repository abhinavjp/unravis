package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// noRepo is a repository that panics on any call, so a test that reaches the
// live audit connection fails. Later tracing tests reuse it.
type noRepo struct{ audit.Repository }

// Sources lists nothing, so the default tracing call needs no live read; every
// other method still panics.
func (noRepo) Sources(context.Context) ([]audit.Source, error) { return []audit.Source{}, nil }

// seedTracingTag adds one source tag to a case and returns it with its id.
func seedTracingTag(t *testing.T, store casestore.Store, caseID string, tag casestore.SourceTag) casestore.SourceTag {
	t.Helper()
	c, err := store.Open(context.Background(), casestore.CaseID(caseID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tagger, ok := c.(casestore.Tagger)
	if !ok {
		t.Fatal("store does not keep source tags")
	}
	out, err := tagger.AddSource(context.Background(), tag)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// seedTracingImport runs one import of the given events into a case. A distinct
// file gives a distinct body, so a second import is never a duplicate.
func seedTracingImport(t *testing.T, store casestore.Store, caseID, importerName, file string, evs []evidence.Event) casestore.ImportRecord {
	t.Helper()
	c, err := store.Open(context.Background(), casestore.CaseID(caseID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	events := make([]evidence.Event, len(evs))
	copy(events, evs)
	for i := range events {
		line := int64(i + 2)
		events[i].Source = importerName
		events[i].Line = line
		events[i].EvidenceID = evidence.EventID(caseID, importerName, file, line, 0)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: importerName}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, ev := range events {
			rep.Rows++
			if err := emit(ev); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	meta := casestore.ImportMeta{Importer: importerName, FileName: file, HostLabel: "web-a", TZRule: clock.RuleUTCField,
		AckGating: true, AckText: "ack"}
	res, err := c.Import(context.Background(), meta, strings.NewReader("body "+file), run)
	if err != nil {
		t.Fatal(err)
	}
	return res.Record
}

// tracingEvents makes n simple events one second apart.
func tracingEvents(n int) []evidence.Event {
	base := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	out := make([]evidence.Event, n)
	for i := range out {
		v := float64(i)
		out[i] = evidence.Event{Kind: "cpu", Host: "web-a", Start: base.Add(time.Duration(i) * time.Second),
			Value: &v, Unit: "percent", TZRule: "utc-field"}
		out[i].End = out[i].Start.Add(time.Second)
	}
	return out
}

// tracingFingerprint is what a read must leave alone: the tags, the imports and
// the number of events.
func tracingFingerprint(t *testing.T, store casestore.Store, caseID string) string {
	t.Helper()
	ctx := context.Background()
	c, err := store.Open(ctx, casestore.CaseID(caseID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var fp struct {
		Tags    []casestore.SourceTag
		Imports []casestore.ImportRecord
		Events  int
	}
	if tagger, ok := c.(casestore.Tagger); ok {
		if fp.Tags, err = tagger.Sources(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fp.Imports, err = c.Imports(ctx); err != nil {
		t.Fatal(err)
	}
	evs, err := c.Events(ctx, casestore.EventQuery{
		Window: importer.Window{From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)},
		Limit:  casestore.MaxEventQuery,
	})
	if err != nil {
		t.Fatal(err)
	}
	fp.Events = len(evs)
	b, err := json.Marshal(fp)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const tracingWindowBody = `"incident":{"start":"2001-02-03T04:00:00Z","end":"2001-02-03T05:00:00Z"}`

func tracingBody(extra string) string {
	if extra == "" {
		return `{` + tracingWindowBody + `}`
	}
	return `{` + extra + `,` + tracingWindowBody + `}`
}

func tracingPath(id string) string { return "/api/cases/" + id + "/tracing" }

func tracingErrorText(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	decodeBody(t, res, &out)
	return out.Error
}

// tracingSourcesOf decodes a 200 answer and returns $.sources.
func tracingSourcesOf(t *testing.T, res *httptest.ResponseRecorder) []tracingRef {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("tracing = %d %s", res.Code, res.Body)
	}
	var out tracingResponse
	decodeBody(t, res, &out)
	return out.Sources
}

func tracingEnv(t *testing.T, store casestore.Store) *caseEnv {
	return tagEnv(t, store, noRepo{})
}

func TestTracingStrictRequest(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		many := make([]string, 51)
		for i := range many {
			many[i] = `"imp_` + strconv.Itoa(i+1) + `"`
		}
		cases := []struct {
			name, body string
			code       int
			text       string
		}{
			{"unknown field route", `{"route":"/x",` + tracingWindowBody + `}`, 400, "Request body has a field this server does not know."},
			{"too many ids", tracingBody(`"sourceIds":[` + strings.Join(many, ",") + `]`), 400, "Pick at most 50 sources."},
			{"empty id", tracingBody(`"sourceIds":["  "]`), 400, "A source id is not valid."},
			{"long id", tracingBody(`"sourceIds":["` + strings.Repeat("a", 129) + `"]`), 400, "A source id is not valid."},
			{"end before start", `{"incident":{"start":"2001-02-03T05:00:00Z","end":"2001-02-03T04:00:00Z"}}`, 400, tracingWindowText},
			{"end equals start", `{"incident":{"start":"2001-02-03T05:00:00Z","end":"2001-02-03T05:00:00Z"}}`, 400, tracingWindowText},
			{"no window", `{}`, 400, tracingWindowText},
			{"year 9999 end", `{"incident":{"start":"9999-12-31T22:00:00Z","end":"9999-12-31T23:30:00Z"}}`, 400, tracingWindowText},
			{"year 0 in UTC", `{"incident":{"start":"0000-01-01T00:30:00+01:00","end":"0000-01-01T02:00:00+01:00"}}`, 400, tracingWindowText},
			{"reference year 9999", `{"incident":{"start":"2001-02-03T04:00:00Z","end":"2001-02-03T05:00:00Z"},"reference":{"start":"9999-12-31T22:00:00Z","end":"9999-12-31T23:30:00Z"}}`, 400, tracingWindowText},
			{"bad reference", tracingBody(`"reference":{"start":"2001-02-03T05:00:00Z","end":"2001-02-03T04:00:00Z"}`), 400, tracingWindowText},
			{"negative seq", tracingBody(`"requestSeq":-1`), 400, "The request number is not valid."},
		}
		for _, c := range cases {
			res := e.json(http.MethodPost, tracingPath(id), c.body)
			if res.Code != c.code {
				t.Errorf("%s = %d %s, want %d", c.name, res.Code, res.Body, c.code)
				continue
			}
			if got := tracingErrorText(t, res); got != c.text {
				t.Errorf("%s says %q, want %q", c.name, got, c.text)
			}
		}
		// A repeated audit id collapses to one: the listing has audit-a, so the
		// request is 200 and the audit read names it once (WP-17a).
		dupRepo := &auditFake{sources: auditSources("audit-a")}
		dupEnv := tagEnv(t, e.store, dupRepo)
		dup := tracingBody(`"sourceIds":["audit-a","audit-a"]`)
		if res := dupEnv.json(http.MethodPost, tracingPath(id), dup); res.Code != http.StatusOK {
			t.Errorf("duplicate ids = %d %s", res.Code, res.Body)
		}
		if len(dupRepo.plans) != 1 || strings.Join(dupRepo.plans[0].SourceIDs, ",") != "audit-a" {
			t.Errorf("duplicate ids read %+v, want one read of audit-a", dupRepo.plans)
		}
		// Wrong Content-Type.
		req := httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingBody("")))
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", "text/plain")
		if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain = %d %s, want 415", res.Code, res.Body)
		}
		// Only POST.
		if res := e.json(http.MethodGet, tracingPath(id), ""); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET = %d, want 405", res.Code)
		}
	})
}

func forEachTracingStore(t *testing.T, fn func(t *testing.T, e *caseEnv, id string)) {
	t.Helper()
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tracingEnv(t, store)
			fn(t, e, e.newCase("tracing"))
		})
	}
}

func TestTracingOmittedSourceIdsMeansAll(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp1 := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingEvents(2))
		tagA := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a logs", ImportID: imp1.ID})
		tagB := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "sql-trace"})
		imp2 := seedTracingImport(t, e.store, id, "sql-xel", "b.xel", tracingEvents(2))
		want := []tracingRef{
			{SourceID: "cs_" + strconv.FormatInt(tagA.ID, 10), Label: "web-a logs"},
			{SourceID: "cs_" + strconv.FormatInt(tagB.ID, 10), Label: "sql-trace"},
			{SourceID: "imp_" + strconv.FormatInt(imp2.ID, 10), Label: "sql-xel"},
		}
		for _, body := range []string{tracingBody(""), tracingBody(`"sourceIds":null`), tracingBody(`"sourceIds":[]`)} {
			res := e.json(http.MethodPost, tracingPath(id), body)
			got := tracingSourcesOf(t, res)
			if len(got) != len(want) {
				t.Fatalf("%s sources = %+v, want %+v", body, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s source %d = %+v, want %+v", body, i, got[i], want[i])
				}
			}
		}
	})

	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		for i := 0; i < 55; i++ {
			seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "t" + strconv.Itoa(i)})
		}
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != http.StatusOK {
			t.Fatalf("55 tags = %d %s", res.Code, res.Body)
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		// WP-18b adds the no-file warning (55 tags with no file yet), after the cap warning.
		if len(out.Sources) != 50 || len(out.Warnings) != 2 || out.Warnings[0] != "Only the first 50 sources are shown." || out.Warnings[1] != tracingNoFileWarn {
			t.Fatalf("55 tags: %d sources, warnings %v", len(out.Sources), out.Warnings)
		}
		if out.Sources[0].Label != "t0" || out.Sources[49].Label != "t49" {
			t.Errorf("not the oldest 50: first %q last %q", out.Sources[0].Label, out.Sources[49].Label)
		}
	})

	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		ids := make([]string, 51)
		for i := range ids {
			ids[i] = `"cs_` + strconv.Itoa(i+1) + `"`
		}
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":[`+strings.Join(ids, ",")+`]`))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("51 explicit ids = %d %s, want 400", res.Code, res.Body)
		}
	})
}

func TestTracingSourceIDs(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		for _, sid := range []string{"cs_9999", "imp_9999"} {
			res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":["`+sid+`"]`))
			if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "unknown-source" {
				t.Errorf("%s = %d %s, want 404 unknown-source", sid, res.Code, res.Body)
			}
			if got := tracingErrorText(t, res); got != "That source is not part of this case." {
				t.Errorf("%s says %q", sid, got)
			}
		}
		for _, sid := range []string{"cs_x1", "imp_0", "cs_", "imp_-3", "cs_1234567890123456789"} {
			res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":["`+sid+`"]`))
			if res.Code != http.StatusBadRequest || tracingErrorText(t, res) != "A source id is not valid." {
				t.Errorf("%s = %d %s, want 400 not valid", sid, res.Code, res.Body)
			}
		}
		tag := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a"})
		imp := seedTracingImport(t, e.store, id, "sql-xel", "c.xel", tracingEvents(1))
		tagID := "cs_" + strconv.FormatInt(tag.ID, 10)
		impID := "imp_" + strconv.FormatInt(imp.ID, 10)
		got := tracingSourcesOf(t, e.json(http.MethodPost, tracingPath(id),
			tracingBody(`"sourceIds":["`+impID+`","`+tagID+`","`+impID+`"]`)))
		want := []tracingRef{{SourceID: impID, Label: "sql-xel"}, {SourceID: tagID, Label: "web-a"}}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("sources = %+v, want %+v", got, want)
		}
		// An audit-looking id is checked against the listed audit sources (WP-17a);
		// noRepo lists none, so it is an unknown source.
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":["demo-west"]`))
		if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "unknown-source" {
			t.Errorf("demo-west = %d %s, want 404 unknown-source", res.Code, res.Body)
		}
	})
}

func TestParseTracingSource(t *testing.T) {
	cases := []struct {
		in   string
		kind string
		n    int64
		ok   bool
	}{
		{"cs_1", "cs", 1, true},
		{"imp_42", "imp", 42, true},
		{"cs_999999999999999999", "cs", 999999999999999999, true},
		{"cs_1000000000000000000", "cs", 0, false},
		{"cs_0", "cs", 0, false},
		{"imp_0", "imp", 0, false},
		{"cs_x1", "cs", 0, false},
		{"imp_", "imp", 0, false},
		{"demo-west", "audit", 0, true},
		{"cs1", "audit", 0, true},
	}
	for _, c := range cases {
		kind, n, ok := parseTracingSource(c.in)
		if kind != c.kind || n != c.n || ok != c.ok {
			t.Errorf("parseTracingSource(%q) = %q %d %v, want %q %d %v", c.in, kind, n, ok, c.kind, c.n, c.ok)
		}
	}
}

func TestUnwrapCaseKeepsOptionalInterfaces(t *testing.T) {
	store, _ := tagStores(t)["sqlite"]()
	e := tracingEnv(t, store)
	id := e.newCase("unwrap")
	c, err := store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wrapper casestore.Case = &sharedCase{Case: c, release: func() {}}
	if _, ok := wrapper.(casestore.Tagger); ok {
		t.Fatal("the wrapper shows Tagger; the test proves nothing")
	}
	if _, ok := unwrapCase(wrapper).(casestore.Tagger); !ok {
		t.Fatal("unwrapCase lost Tagger")
	}
	if got := unwrapCase(c); got != c {
		t.Fatal("unwrapCase changed an unwrapped handle")
	}
}

// failOpenStore opens no case: it answers err instead.
type failOpenStore struct {
	casestore.Store
	err error
}

func (s *failOpenStore) Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.Store.Open(ctx, id)
}

func TestTracingCaseStates(t *testing.T) {
	// The case exists, but the store refuses to open it.
	store := &failOpenStore{Store: casestore.NewMemory()}
	e := tracingEnv(t, store)
	id := e.newCase("states")
	for _, c := range []struct {
		name   string
		err    error
		code   int
		reason string
	}{
		{"closed", casestore.ErrCaseClosed, http.StatusConflict, "case-closed"},
		{"freeing", casestore.ErrFreeing, http.StatusConflict, "case-freeing"},
		{"locked", casestore.ErrCaseLocked, http.StatusLocked, "case-locked"},
		{"missing", casestore.ErrNotFound, http.StatusNotFound, "case-not-found"},
	} {
		store.err = c.err
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != c.code || failureOf(t, res).Reason != c.reason {
			t.Errorf("%s = %d %s, want %d %s", c.name, res.Code, res.Body, c.code, c.reason)
		}
	}
	store.err = nil

	// A real closed case still reads.
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK && res.Code != http.StatusNoContent {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
	if res.Code != http.StatusOK && res.Code != http.StatusConflict {
		t.Errorf("closed case = %d %s", res.Code, res.Body)
	}

	// Cases unavailable: a server with no case store.
	h := New(noRepo{}, testAssets(), testToken, false)
	req := httptest.NewRequest(http.MethodPost, tracingPath(absentCaseID), strings.NewReader(tracingBody("")))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:8765"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || failureOf(t, rec).Reason != "cases-unavailable" {
		t.Errorf("no cases = %d %s, want 503 cases-unavailable", rec.Code, rec.Body)
	}

	// A bad case id and an absent case.
	if res := e.json(http.MethodPost, tracingPath("not-an-id"), tracingBody("")); res.Code != http.StatusBadRequest {
		t.Errorf("bad case id = %d, want 400", res.Code)
	}
	if res := e.json(http.MethodPost, tracingPath(absentCaseID), tracingBody("")); res.Code != http.StatusNotFound {
		t.Errorf("absent case = %d %s, want 404", res.Code, res.Body)
	}
	// No token.
	req = httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingBody("")))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:8765"
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
}

func TestTracingEnvelope(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"requestSeq":7`))
		if res.Code != http.StatusOK {
			t.Fatalf("tracing = %d %s", res.Code, res.Body)
		}
		dec := json.NewDecoder(bytes.NewReader(res.Body.Bytes()))
		dec.DisallowUnknownFields()
		var out tracingResponse
		if err := dec.Decode(&out); err != nil {
			t.Fatalf("body does not decode into tracingResponse: %v: %s", err, res.Body)
		}
		if out.CaseID != id || out.RequestSeq == nil || *out.RequestSeq != 7 {
			t.Errorf("caseId %q requestSeq %v", out.CaseID, out.RequestSeq)
		}
		start := time.Date(2001, 2, 3, 4, 0, 0, 0, time.UTC)
		if !out.Window.Start.Equal(start) || !out.Window.End.Equal(start.Add(time.Hour)) {
			t.Errorf("window %+v", out.Window)
		}
		if out.AsOf.IsZero() || out.Truncated || out.MixedConsistency || !out.AllLanesStored {
			t.Errorf("meta %+v", out)
		}
		body := res.Body.String()
		for _, key := range []string{`"key":"sql"`, `"key":"iis"`, `"state":"no-data"`, `"warnings":[]`, `"sources":[]`} {
			if !strings.Contains(body, key) {
				t.Errorf("body lacks %s: %s", key, body)
			}
		}
		for _, bad := range []string{`":null`} {
			if strings.Contains(body, bad) {
				t.Errorf("body holds a null: %s", body)
			}
		}
		for _, key := range []string{"axisStart", "axisEnd", "bucketSeconds", "buckets"} {
			if !strings.Contains(body, `"`+key+`"`) {
				t.Errorf("window lacks %s: %s", key, body)
			}
		}
		// requestSeq is absent when the request sent none.
		res = e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "requestSeq") {
			t.Errorf("no seq sent: %d %s", res.Code, res.Body)
		}
		for _, banned := range []string{"client-ip", "referer", "cookie"} {
			if strings.Contains(strings.ToLower(res.Body.String()), banned) {
				t.Errorf("body holds %q", banned)
			}
		}
	})
}

func TestTracingOpensNoLiveConnection(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		tag := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a"})
		imp := seedTracingImport(t, e.store, id, "sql-xel", "d.xel", tracingEvents(1))
		bodies := []string{
			tracingBody(""),
			tracingBody(`"sourceIds":["cs_` + strconv.FormatInt(tag.ID, 10) + `","imp_` + strconv.FormatInt(imp.ID, 10) + `"]`),
			tracingBody(`"sourceIds":["demo-west"]`),
			tracingBody(`"sourceIds":["cs_9999"]`),
			tracingBody(`"sourceIds":["cs_x1"]`),
			`{"route":"x"}`,
			`{}`,
		}
		for _, b := range bodies {
			// A call on noRepo panics the handler goroutine; the recorder would not hide that.
			e.json(http.MethodPost, tracingPath(id), b)
		}
	})
}

func TestTracingReadOnly(t *testing.T) {
	store, _ := tagStores(t)["sqlite"]()
	e := tracingEnv(t, store)
	id := e.newCase("readonly")
	imp := seedTracingImport(t, store, id, "iis-w3c", "e.log", tracingEvents(3))
	seedTracingTag(t, store, id, casestore.SourceTag{Kind: "iis", Label: "web-a", ImportID: imp.ID})
	before := tracingFingerprint(t, store, id)
	if res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"requestSeq":1`)); res.Code != http.StatusOK {
		t.Fatalf("tracing = %d %s", res.Code, res.Body)
	}
	if after := tracingFingerprint(t, store, id); after != before {
		t.Fatalf("a tracing read changed the case:\nbefore %s\nafter  %s", before, after)
	}
}

func tracingWindowBodyFor(start, end string) string {
	return `{"incident":{"start":"` + start + `","end":"` + end + `"}}`
}

func TestTracingWindowLimit(t *testing.T) {
	const tooLong = "A time window can be at most 30 days long."
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		ok := []struct{ name, start, end string }{
			{"exactly 30 days", "2026-03-10T00:00:00Z", "2026-04-09T00:00:00Z"},
			{"29 days 23 h 59 m", "2026-03-10T00:00:00Z", "2026-04-08T23:59:00Z"},
			{"30 days across a US clock change", "2026-03-01T00:00:00Z", "2026-03-31T00:00:00Z"},
		}
		for _, c := range ok {
			res := e.json(http.MethodPost, tracingPath(id), tracingWindowBodyFor(c.start, c.end))
			if res.Code != http.StatusOK {
				t.Errorf("%s = %d %s", c.name, res.Code, res.Body)
			}
		}
		bad := []struct{ name, body, text string }{
			{"30 days and 1 s", tracingWindowBodyFor("2026-03-10T00:00:00Z", "2026-04-09T00:00:01Z"), tooLong},
			{"a year", tracingWindowBodyFor("2025-03-10T00:00:00Z", "2026-03-10T00:00:00Z"), tooLong},
			{"zero span", tracingWindowBodyFor("2026-03-10T00:00:00Z", "2026-03-10T00:00:00Z"), tracingWindowText},
			{"negative span", tracingWindowBodyFor("2026-03-10T00:00:00Z", "2026-03-09T00:00:00Z"), tracingWindowText},
			{"reference over 30 days", `{"incident":{"start":"2026-03-10T00:00:00Z","end":"2026-03-10T01:00:00Z"},"reference":{"start":"2026-02-01T00:00:00Z","end":"2026-03-10T00:00:00Z"}}`, tooLong},
		}
		for _, c := range bad {
			res := e.json(http.MethodPost, tracingPath(id), c.body)
			if res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d %s, want 400", c.name, res.Code, res.Body)
				continue
			}
			if got := tracingErrorText(t, res); got != c.text {
				t.Errorf("%s says %q, want %q", c.name, got, c.text)
			}
		}
		// A reference of exactly 30 days is fine.
		res := e.json(http.MethodPost, tracingPath(id), `{"incident":{"start":"2026-03-10T00:00:00Z","end":"2026-03-10T01:00:00Z"},"reference":{"start":"2026-02-08T00:00:00Z","end":"2026-03-10T00:00:00Z"}}`)
		if res.Code != http.StatusOK {
			t.Errorf("reference of 30 days = %d %s", res.Code, res.Body)
		}
	})
}

func TestTracingResponseWindow(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, tracingPath(id), tracingWindowBodyFor("2026-03-10T10:07:00Z", "2026-03-10T11:07:00Z"))
		if res.Code != http.StatusOK {
			t.Fatalf("tracing = %d %s", res.Code, res.Body)
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		w := out.Window
		if w.BucketSeconds != 60 || w.Buckets != 120 {
			t.Errorf("bucketSeconds %d buckets %d, want 60 and 120", w.BucketSeconds, w.Buckets)
		}
		if !w.AxisStart.Equal(time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)) || !w.AxisEnd.Equal(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)) {
			t.Errorf("axis %v .. %v", w.AxisStart, w.AxisEnd)
		}
		if !w.Start.Equal(time.Date(2026, 3, 10, 10, 7, 0, 0, time.UTC)) || !w.End.Equal(time.Date(2026, 3, 10, 11, 7, 0, 0, time.UTC)) {
			t.Errorf("window %v .. %v", w.Start, w.End)
		}
		body := res.Body.String()
		for _, want := range []string{`"axisStart":"2026-03-10T10:00:00Z"`, `"axisEnd":"2026-03-10T12:00:00Z"`, `"bucketSeconds":60`, `"buckets":120`} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %s: %s", want, body)
			}
		}
		// A request in another zone offset still gets a UTC axis.
		res = e.json(http.MethodPost, tracingPath(id), tracingWindowBodyFor("2026-03-10T15:37:00+05:30", "2026-03-10T16:37:00+05:30"))
		var other tracingResponse
		decodeBody(t, res, &other)
		if !other.Window.AxisStart.Equal(w.AxisStart) || other.Window.BucketSeconds != 60 {
			t.Errorf("offset window axis %+v", other.Window)
		}
		if !strings.Contains(res.Body.String(), `"axisStart":"2026-03-10T10:00:00Z"`) {
			t.Errorf("axis not in UTC: %s", res.Body)
		}
	})
}

// ---- WP-18b: saved SQL and IIS lanes ----

var tracingLaneT0 = time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)

const tracingLaneWindow = `"incident":{"start":"2026-03-10T10:00:00Z","end":"2026-03-10T11:00:00Z"}`

// tracingKindEvents makes n events of one kind, step apart from start. The end
// equals the start. attrs are copied into every event. (The packet names this
// builder tracingEvents; that name is taken by the WP-14 helper, so it is
// tracingKindEvents here.)
func tracingKindEvents(kind string, start time.Time, n int, step time.Duration, attrs map[string]string) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		at := start.Add(time.Duration(i) * step)
		out[i] = evidence.Event{Kind: kind, Host: "web-a", Start: at, End: at, TZRule: clock.RuleUTCField}
		if len(attrs) > 0 {
			out[i].Attrs = make(map[string]string, len(attrs))
			for k, v := range attrs {
				out[i].Attrs[k] = v
			}
		}
	}
	return out
}

func tracingLaneBody(ids ...string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = `"` + id + `"`
	}
	return `{"sourceIds":[` + strings.Join(quoted, ",") + `],` + tracingLaneWindow + `}`
}

// tracingPost posts a body, requires 200 and returns the decoded and raw answer.
func tracingPost(t *testing.T, e *caseEnv, id, body string) (tracingResponse, string) {
	t.Helper()
	res := e.json(http.MethodPost, tracingPath(id), body)
	if res.Code != http.StatusOK {
		t.Fatalf("tracing = %d %s", res.Code, res.Body)
	}
	var out tracingResponse
	decodeBody(t, res, &out)
	raw := res.Body.String()
	for _, key := range []string{"lanes", "warnings", "sources", "buckets", "events"} {
		if strings.Contains(raw, `"`+key+`":null`) {
			t.Errorf("%s is null: %s", key, raw)
		}
	}
	return out, raw
}

func tracingLaneOf(t *testing.T, out tracingResponse, key string) tracingLane {
	t.Helper()
	for _, l := range out.Lanes {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no lane %q in %+v", key, out.Lanes)
	return tracingLane{}
}

func tracingBucketSum(l tracingLane) int64 {
	var n int64
	for _, b := range l.Buckets {
		n += b.Count
	}
	return n
}

func sqliteTracingEnv(t *testing.T) (*caseEnv, string) {
	t.Helper()
	store, _ := tagStores(t)["sqlite"]()
	e := tracingEnv(t, store)
	return e, e.newCase("tracing")
}

func TestTracingSQLIILanes(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		sqlImp := seedTracingImport(t, e.store, id, "sql-xel", "a.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(10*time.Minute), 3, time.Minute, nil))
		iisImp := seedTracingImport(t, e.store, id, "iis-w3c", "b.log", tracingKindEvents("request", tracingLaneT0.Add(20*time.Minute), 5, time.Minute, map[string]string{"status": "503"}))
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(sqlImp.ID), tracingImportID(iisImp.ID)))
		if len(out.Lanes) != 2 || out.Lanes[0].Key != "sql" || out.Lanes[1].Key != "iis" {
			t.Fatalf("lanes %+v", out.Lanes)
		}
		for key, want := range map[string]struct {
			sum int64
			imp casestore.ImportRecord
		}{"sql": {3, sqlImp}, "iis": {5, iisImp}} {
			l := tracingLaneOf(t, out, key)
			if l.State != "events" || l.Origin != "case" || l.Consistency != "stored-as-of" || !l.Complete || l.Truncated || l.ReasonCode != "" || l.Reason != "" {
				t.Errorf("%s meta %+v", key, l)
			}
			if got := tracingBucketSum(l); got != want.sum || l.Scanned != want.sum {
				t.Errorf("%s bucket sum %d scanned %d, want %d", key, got, l.Scanned, want.sum)
			}
			if !l.AsOf.Truncate(time.Millisecond).Equal(want.imp.Imported.UTC().Truncate(time.Millisecond)) {
				t.Errorf("%s asOf %v, want import time %v", key, l.AsOf, want.imp.Imported)
			}
			if len(l.Events) != int(want.sum) || len(l.Events) > calltrace.MaxLaneEvents {
				t.Errorf("%s has %d events", key, len(l.Events))
			}
			if len(l.Sources) != 1 || l.Sources[0].SourceID != tracingImportID(want.imp.ID) {
				t.Errorf("%s sources %+v", key, l.Sources)
			}
			for _, ev := range l.Events {
				if ev.SourceID != tracingImportID(want.imp.ID) {
					t.Errorf("%s event sourceId %q", key, ev.SourceID)
				}
			}
			for _, b := range l.Buckets {
				if !b.Complete {
					t.Errorf("%s bucket not complete: %+v", key, b)
				}
			}
		}
		if got := tracingLaneOf(t, out, "iis").Events[0].StatusClass; got != "5xx" {
			t.Errorf("statusClass %q", got)
		}
		if !out.AllLanesStored || out.MixedConsistency || out.Truncated {
			t.Errorf("meta stored=%v mixed=%v truncated=%v", out.AllLanesStored, out.MixedConsistency, out.Truncated)
		}
		// The iis lane carries errors on every bucket (the five rows are all 503);
		// the sql lane never does.
		var iisErrs int64
		for _, b := range tracingLaneOf(t, out, "iis").Buckets {
			if b.Errors == nil {
				t.Fatalf("iis bucket without errors: %+v", b)
			}
			iisErrs += *b.Errors
		}
		if iisErrs != 5 {
			t.Errorf("iis errors %d, want 5", iisErrs)
		}
		for _, b := range tracingLaneOf(t, out, "sql").Buckets {
			if b.Errors != nil {
				t.Errorf("sql bucket carries errors: %+v", b)
			}
		}
		if len(out.Warnings) != 0 {
			t.Errorf("warnings %v", out.Warnings)
		}
		_ = raw
	})
}

// TestTracingIISBucketErrors checks the per-bucket errors of the iis lane on both
// stores: only 5xx rows count (exactly three digits, first 5), 4xx, 200 and a row
// without a status do not, an HTTP.sys marker with a 503 does not, and a bucket
// without errors still says 0. The sql lane has no errors key at all.
func TestTracingIISBucketErrors(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		at := func(min int, status string) evidence.Event {
			ev := evidence.Event{Kind: "request", Host: "web-a", Start: tracingLaneT0.Add(time.Duration(min) * time.Minute), TZRule: clock.RuleUTCField}
			ev.End = ev.Start
			if status != "" {
				ev.Attrs = map[string]string{"status": status}
			}
			return ev
		}
		rows := []evidence.Event{
			at(1, "200"), at(1, "503"), at(1, "500"), at(1, "404"), // minute 1: 4 rows, 2 errors
			at(2, "502"), at(2, ""), at(2, "5xx"), at(2, "5000"), // minute 2: 4 rows, 1 error
			at(30, "301"), // minute 30: 1 row, 0 errors
		}
		iisImp := seedTracingImport(t, e.store, id, "iis-w3c", "w.log", rows)
		marker := at(1, "503")
		marker.Kind = "httperr"
		marker.Attrs["reason"] = "Timer_ConnectionIdle"
		errImp := seedTracingImport(t, e.store, id, "iis-httperr", "h.log", []evidence.Event{marker})
		sqlImp := seedTracingImport(t, e.store, id, "sql-xel", "s.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(time.Minute), 2, time.Second, map[string]string{"status": "503"}))
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(iisImp.ID), tracingImportID(errImp.ID), tracingImportID(sqlImp.ID)))

		iis := tracingLaneOf(t, out, "iis")
		got := map[int]int64{}
		var total, errTotal int64
		for _, b := range iis.Buckets {
			if b.Errors == nil {
				t.Fatalf("iis bucket without errors key: %+v", b)
			}
			if *b.Errors > b.Count {
				t.Errorf("errors %d above count %d", *b.Errors, b.Count)
			}
			got[int(b.Start.Sub(tracingLaneT0)/time.Minute)] = *b.Errors
			total += b.Count
			errTotal += *b.Errors
		}
		if total != 10 { // 9 requests and 1 marker
			t.Errorf("iis count %d, want 10", total)
		}
		if errTotal != 3 || got[1] != 2 || got[2] != 1 || got[30] != 0 {
			t.Errorf("iis errors by minute %v total %d, want minute1=2 minute2=1 total=3", got, errTotal)
		}
		for _, b := range tracingLaneOf(t, out, "sql").Buckets {
			if b.Errors != nil {
				t.Errorf("sql bucket carries errors: %+v", b)
			}
		}
	})
}

// TestTracingRowCapIsPerLane: a 60 000 row iis lane must not starve the sql lane
// of its 3 001 events or flag it incomplete; only the iis lane (whose own rows
// passed the cap) is truncated.
func TestTracingRowCapIsPerLane(t *testing.T) {
	if testing.Short() {
		t.Skip("imports 63 001 events")
	}
	e, id := sqliteTracingEnv(t)
	const iisN, sqlN = 60000, 3001
	iisImp := seedTracingImport(t, e.store, id, "iis-w3c", "big.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), iisN, time.Millisecond, map[string]string{"status": "200"}))
	sqlImp := seedTracingImport(t, e.store, id, "sql-xel", "s.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(30*time.Minute), sqlN, time.Millisecond, nil))
	out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(sqlImp.ID), tracingImportID(iisImp.ID)))

	sql := tracingLaneOf(t, out, "sql")
	if sql.State != "events" || sql.Truncated || sql.ReasonCode != "" || sql.Reason != "" || !sql.Complete {
		t.Errorf("sql lane flagged: %+v", sql)
	}
	if sql.Scanned != sqlN || tracingBucketSum(sql) != sqlN {
		t.Errorf("sql scanned %d bucket sum %d, want %d", sql.Scanned, tracingBucketSum(sql), sqlN)
	}
	if len(sql.Events) == 0 || len(sql.Events) > calltrace.MaxLaneEvents || len(sql.Events)+sql.Hidden != sqlN {
		t.Errorf("sql lists %d events and hides %d, want %d together", len(sql.Events), sql.Hidden, sqlN)
	}
	iis := tracingLaneOf(t, out, "iis")
	if !iis.Truncated || iis.ReasonCode != "rowcap" {
		t.Errorf("iis lane not flagged: %+v", iis)
	}
	if iis.Scanned != iisN || tracingBucketSum(iis) != iisN {
		t.Errorf("iis scanned %d bucket sum %d, want %d", iis.Scanned, tracingBucketSum(iis), iisN)
	}
	if !out.Truncated {
		t.Error("top-level truncated is false")
	}
}

// TestTracingBucketsUseGroupBy proves the counts come from the store's own
// GROUP BY: the handle given to the counter is the unwrapped one.
func TestTracingBucketsUseGroupBy(t *testing.T) {
	orig := tracingCountBuckets
	t.Cleanup(func() { tracingCountBuckets = orig })
	var seen []bool
	tracingCountBuckets = func(ctx context.Context, c casestore.Case, q casestore.BucketQuery) (casestore.BucketResult, error) {
		_, isCounter := c.(casestore.BucketCounter)
		seen = append(seen, isCounter)
		return casestore.CountBuckets(ctx, c, q)
	}
	run := func(store casestore.Store) {
		e := tracingEnv(t, store)
		id := e.newCase("groupby")
		s := seedTracingImport(t, store, id, "sql-xel", "a.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(time.Minute), 2, time.Minute, nil))
		i := seedTracingImport(t, store, id, "iis-w3c", "b.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 2, time.Minute, nil))
		seen = nil
		tracingPost(t, e, id, tracingLaneBody(tracingImportID(s.ID), tracingImportID(i.ID)))
	}
	t.Run("sqlite", func(t *testing.T) {
		store, _ := tagStores(t)["sqlite"]()
		run(store)
		if len(seen) != 2 {
			t.Fatalf("spy called %d times, want 2", len(seen))
		}
		for i, ok := range seen {
			if !ok {
				t.Errorf("call %d saw a handle that is not a BucketCounter (unwrapCase skipped)", i)
			}
		}
	})
	t.Run("memory", func(t *testing.T) {
		run(casestore.NewMemory())
		if len(seen) != 2 {
			t.Fatalf("spy called %d times, want 2", len(seen))
		}
		for i, ok := range seen {
			if ok {
				t.Errorf("call %d: the memory store is not a BucketCounter, the spy cannot tell the paths apart", i)
			}
		}
	})
}

func TestTracingBucketsExactPastRowCap(t *testing.T) {
	if testing.Short() {
		t.Skip("imports 50 001 events")
	}
	e, id := sqliteTracingEnv(t)
	const n = casestore.MaxEventQuery + 1
	imp := seedTracingImport(t, e.store, id, "iis-w3c", "big.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), n, time.Millisecond, nil))
	out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
	l := tracingLaneOf(t, out, "iis")
	if got := tracingBucketSum(l); got != n || l.Scanned != n {
		t.Errorf("bucket sum %d scanned %d, want %d", got, l.Scanned, n)
	}
	if !l.Truncated || l.ReasonCode != "rowcap" || l.Reason != "Only the first 50,000 saved events were listed; the bucket counts cover every saved event." {
		t.Errorf("lane meta %+v", l)
	}
	if len(l.Events) > calltrace.MaxLaneEvents || !out.Truncated {
		t.Errorf("events %d top-level truncated %v", len(l.Events), out.Truncated)
	}
	if sql := tracingLaneOf(t, out, "sql"); sql.State != "no-data" || sql.Truncated {
		t.Errorf("sql lane %+v", sql)
	}
}

func TestTracingClockUnchecked(t *testing.T) {
	stub := &stubTraceCase{imports: []casestore.ImportRecord{
		{ID: 1, Importer: "iis-w3c", Status: casestore.StatusOK, TZRule: ""},
	}}
	sel := tracingSelection{
		Refs:     []tracingRef{{SourceID: "imp_1", Label: "iis-w3c"}},
		ImportOf: map[string]int64{"imp_1": 1},
	}
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	lanes, _, err := tracingSQLIISLanes(context.Background(), stub, axis, sel, tracingLaneT0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lanes) != 2 || lanes[0].Key != "sql" || lanes[1].Key != "iis" {
		t.Fatalf("lanes %+v", lanes)
	}
	iis := lanes[1]
	if iis.State != "clock-unchecked" || iis.Disabled != "tz-rule" ||
		iis.Reason != "The time zone rule of a file is not confirmed, so its events are not placed on the timeline." {
		t.Errorf("iis lane %+v", iis)
	}
	if iis.Buckets == nil || len(iis.Buckets) != 0 || iis.Events == nil || len(iis.Events) != 0 {
		t.Errorf("iis buckets %v events %v, want empty non-nil", iis.Buckets, iis.Events)
	}
	if lanes[0].State != "no-data" || lanes[0].Buckets == nil || lanes[0].Events == nil {
		t.Errorf("sql lane %+v", lanes[0])
	}
	if len(stub.queries) != 0 {
		t.Errorf("an event read ran: %+v", stub.queries)
	}
	raw, err := json.Marshal(lanes)
	if err != nil || !strings.Contains(string(raw), `"buckets":[]`) || !strings.Contains(string(raw), `"events":[]`) {
		t.Errorf("json %s %v", raw, err)
	}
}

func TestTracingAmbiguousSource(t *testing.T) {
	e, id := sqliteTracingEnv(t)
	a := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 3, time.Minute, nil))
	b := seedTracingImport(t, e.store, id, "iis-w3c", "b.log", tracingKindEvents("request", tracingLaneT0.Add(30*time.Minute), 4, time.Minute, nil))
	out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(a.ID), tracingImportID(b.ID)))
	want := "Events from iis-w3c cannot be tied to one source because two selected files use it."
	if len(out.Warnings) != 1 || out.Warnings[0] != want {
		t.Errorf("warnings %q, want [%q]", out.Warnings, want)
	}
	l := tracingLaneOf(t, out, "iis")
	if len(l.Events) != 7 {
		t.Fatalf("events %d", len(l.Events))
	}
	for _, ev := range l.Events {
		if ev.SourceID != "" {
			t.Errorf("event sourceId %q, want empty", ev.SourceID)
		}
	}
	if got := tracingBucketSum(l); got != 7 || l.Scanned != 7 || len(l.Sources) != 2 {
		t.Errorf("bucket sum %d scanned %d sources %+v", got, l.Scanned, l.Sources)
	}
}

func TestTracingUnselectedImportNotCounted(t *testing.T) {
	e, id := sqliteTracingEnv(t)
	a := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 5, time.Minute, nil))
	seedTracingImport(t, e.store, id, "iis-httperr", "b.log", tracingKindEvents("httperr", tracingLaneT0.Add(10*time.Minute), 4, time.Minute, nil))
	body := tracingLaneBody(tracingImportID(a.ID))
	out, _ := tracingPost(t, e, id, body)
	l := tracingLaneOf(t, out, "iis")
	if got := tracingBucketSum(l); got != 5 || l.Scanned != 5 {
		t.Errorf("bucket sum %d scanned %d, want 5", got, l.Scanned)
	}
	bIDs := map[string]bool{}
	for line := int64(2); line < 6; line++ {
		bIDs[evidence.EventID(id, "iis-httperr", "b.log", line, 0)] = true
	}
	for _, ev := range l.Events {
		if bIDs[ev.ID] || ev.Label == "HTTP.sys error" {
			t.Errorf("an event of the unselected import is listed: %+v", ev)
		}
	}
	if len(l.Events) != 5 {
		t.Errorf("events %d, want 5", len(l.Events))
	}

	// Limitation: the event read filters by importer NAME and an event carries no
	// import id. A third import with the same importer name as the selected one
	// can have its events listed in events[]. The counts filter by ImportID, so
	// the bucket sum and scanned stay 5 (selected imports only); scanned can be
	// smaller than the number of listed events, so len(events) is not compared
	// with scanned here.
	seedTracingImport(t, e.store, id, "iis-w3c", "c.log", tracingKindEvents("request", tracingLaneT0.Add(40*time.Minute), 4, time.Minute, nil))
	out, _ = tracingPost(t, e, id, body)
	l = tracingLaneOf(t, out, "iis")
	if got := tracingBucketSum(l); got != 5 || l.Scanned != 5 {
		t.Errorf("same-importer case: bucket sum %d scanned %d, want 5", got, l.Scanned)
	}
	if len(l.Events) < 5 || len(l.Events) > 9 {
		t.Errorf("same-importer case lists %d events, want 5 to 9", len(l.Events))
	}
}

func TestTracingNoLookback(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		before := tracingKindEvents("slow-op", tracingLaneT0.Add(-5*time.Minute), 1, time.Minute, nil)
		before[0].End = tracingLaneT0.Add(2 * time.Minute) // starts before, ends inside
		atEnd := tracingKindEvents("slow-op", tracingLaneT0.Add(time.Hour), 1, time.Minute, nil)
		imp := seedTracingImport(t, e.store, id, "sql-xel", "a.xel", append(before, atEnd...))
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		l := tracingLaneOf(t, out, "sql")
		if len(l.Events) != 0 || tracingBucketSum(l) != 0 || l.Scanned != 0 {
			t.Errorf("events %d bucket sum %d scanned %d, want none", len(l.Events), tracingBucketSum(l), l.Scanned)
		}
	})
}

func TestTracingNoPrivateAttrs(t *testing.T) {
	canary := map[string]string{
		"client-ip":      "10.9.8.7",
		"server-ip":      "192.0.2.55",
		"referer":        "https://ref.example/CANARYREF",
		"user-agent":     "Mozilla/CANARYUA",
		"cs-uri-query":   "token=CANARYQUERY",
		"cookie":         "CANARYCOOKIE=1",
		"cookie:session": "CANARYSESSION",
		"status":         "404",
		"win32":          "2",
	}
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		sqlImp := seedTracingImport(t, e.store, id, "sql-xel", "private-sql.xel", tracingKindEvents("slow-op", tracingLaneT0.Add(time.Minute), 2, time.Minute, canary))
		iisImp := seedTracingImport(t, e.store, id, "iis-w3c", "private-web.log", tracingKindEvents("request", tracingLaneT0.Add(time.Minute), 3, time.Minute, canary))
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(sqlImp.ID), tracingImportID(iisImp.ID)))
		whole, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{raw, string(whole)} {
			lower := strings.ToLower(body)
			for _, banned := range []string{"client-ip", "server-ip", "referer", "user-agent", "cs-uri-query", "cookie",
				"10.9.8.7", "192.0.2.55", "canary", "private-sql", "private-web", ".xel", ".log", "mozilla"} {
				if strings.Contains(lower, banned) {
					t.Errorf("response holds %q: %s", banned, body)
				}
			}
		}
		// The two allowed attributes still arrive, as status class and code.
		ev := tracingLaneOf(t, out, "iis").Events[0]
		if ev.StatusClass != "4xx" || ev.Win32 == nil || *ev.Win32 != 2 {
			t.Errorf("iis event %+v", ev)
		}
	})
}

func TestAppendLaneKeepsOrder(t *testing.T) {
	var resp tracingResponse
	keys := func() string {
		var ks []string
		for _, l := range resp.Lanes {
			ks = append(ks, l.Key)
		}
		return strings.Join(ks, ",")
	}
	appendLane(&resp, tracingLane{Key: "iis"})
	if resp.Lanes == nil {
		t.Fatal("Lanes is nil after the first appendLane")
	}
	appendLane(&resp, tracingLane{Key: "sql"})
	appendLane(&resp, tracingLane{Key: "audit"})
	if got := keys(); got != "audit,sql,iis" {
		t.Fatalf("order %s", got)
	}
	appendLane(&resp, tracingLane{Key: "sql", State: "replaced"})
	if got := keys(); got != "audit,sql,iis" || resp.Lanes[1].State != "replaced" {
		t.Errorf("replace: order %s lanes %+v", got, resp.Lanes)
	}
	appendLane(&resp, tracingLane{Key: "zzz"})
	if got := keys(); got != "audit,sql,iis,zzz" {
		t.Errorf("unknown key order %s", got)
	}
	for _, l := range resp.Lanes {
		if l.Sources == nil || l.Buckets == nil || l.Events == nil {
			t.Errorf("lane %s has a nil array", l.Key)
		}
	}
}

func TestTracingMixedMeta(t *testing.T) {
	caseLane := func(key, state string) tracingLane {
		return tracingLane{Key: key, State: state, Origin: "case", Consistency: "stored-as-of"}
	}
	liveLane := func(state string) tracingLane {
		return tracingLane{Key: "audit", State: state, Origin: "live", Consistency: "live-non-snapshot"}
	}
	var resp tracingResponse
	finishTracingMeta(&resp)
	if resp.AllLanesStored || resp.MixedConsistency || resp.Truncated {
		t.Errorf("empty response: %+v", resp)
	}
	appendLane(&resp, caseLane("sql", "events"))
	appendLane(&resp, caseLane("iis", "none-seen"))
	finishTracingMeta(&resp)
	if resp.MixedConsistency || !resp.AllLanesStored || resp.Truncated {
		t.Errorf("case lanes only: %+v", resp)
	}
	appendLane(&resp, liveLane("no-data"))
	finishTracingMeta(&resp)
	if resp.MixedConsistency || resp.AllLanesStored {
		t.Errorf("live lane without data: mixed=%v stored=%v, want false/false", resp.MixedConsistency, resp.AllLanesStored)
	}
	appendLane(&resp, liveLane("events"))
	finishTracingMeta(&resp)
	finishTracingMeta(&resp) // idempotent
	if !resp.MixedConsistency || resp.AllLanesStored {
		t.Errorf("live lane with events: mixed=%v stored=%v, want true/false", resp.MixedConsistency, resp.AllLanesStored)
	}
	truncated := caseLane("iis", "incomplete")
	truncated.Truncated = true
	appendLane(&resp, truncated)
	finishTracingMeta(&resp)
	if !resp.Truncated {
		t.Error("a truncated lane did not set the top-level truncated")
	}
}

// ---- WP-19: HTTP.sys error markers on the iis lane ----

// httperrEventAt makes one iis-httperr event at tracingLaneT0 + offset, a
// point (End == Start) carrying only status and reason.
func httperrEventAt(offset time.Duration, status, reason string) evidence.Event {
	at := tracingLaneT0.Add(offset)
	return evidence.Event{Kind: "httperr", Host: "web-a", Start: at, End: at, TZRule: clock.RuleUTCField,
		Attrs: map[string]string{"status": status, "reason": reason}}
}

// TestTracingHTTPERRMarker proves the reasonByID lookup by EvidenceID reaches
// the right event: two rows at different times get their own, differing
// labels.
func TestTracingHTTPERRMarker(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := []evidence.Event{
			httperrEventAt(30*time.Minute, "503", "QueueFull"),
			httperrEventAt(40*time.Minute, "503", "Timer_AppPool"),
		}
		imp := seedTracingImport(t, e.store, id, "iis-httperr", "a.log", evs)
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		l := tracingLaneOf(t, out, "iis")
		if len(l.Events) != 2 {
			t.Fatalf("events %d, want 2: %+v", len(l.Events), l.Events)
		}
		ev0, ev1 := l.Events[0], l.Events[1]
		if ev0.Kind != "iis-httperr" || ev1.Kind != "iis-httperr" {
			t.Errorf("kind %q / %q, want iis-httperr both", ev0.Kind, ev1.Kind)
		}
		if ev0.StatusClass != "5xx" || ev1.StatusClass != "5xx" {
			t.Errorf("statusClass %q / %q, want 5xx both", ev0.StatusClass, ev1.StatusClass)
		}
		if !ev0.Start.Equal(ev0.End) || !ev1.Start.Equal(ev1.End) {
			t.Errorf("start != end: %+v / %+v", ev0, ev1)
		}
		if ev0.Label != "HTTP.sys error: QueueFull" {
			t.Errorf("ev0 label %q, want %q", ev0.Label, "HTTP.sys error: QueueFull")
		}
		if ev1.Label != "HTTP.sys error: Timer_AppPool" {
			t.Errorf("ev1 label %q, want %q", ev1.Label, "HTTP.sys error: Timer_AppPool")
		}
		if ev0.Label == ev1.Label {
			t.Errorf("both events got the same label %q", ev0.Label)
		}
	})
}

// TestTracingHTTPERRPointInWindow checks the point rule and the label text
// for a single in-window row.
func TestTracingHTTPERRPointInWindow(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-httperr", "a.log",
			[]evidence.Event{httperrEventAt(30*time.Minute, "503", "Connection_Dropped")})
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		l := tracingLaneOf(t, out, "iis")
		if len(l.Events) != 1 {
			t.Fatalf("events %d, want 1: %+v", len(l.Events), l.Events)
		}
		ev := l.Events[0]
		if !ev.Start.Equal(ev.End) {
			t.Errorf("start %v != end %v", ev.Start, ev.End)
		}
		if !strings.Contains(ev.Label, "Connection_Dropped") {
			t.Errorf("label %q does not contain %q", ev.Label, "Connection_Dropped")
		}
	})
}

// TestTracingHTTPERROutsideWindow: a row on the next day, and a row at
// exactly the window end, are both outside the half-open window.
func TestTracingHTTPERROutsideWindow(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := []evidence.Event{
			httperrEventAt(24*time.Hour, "503", "QueueFull"), // next day
			httperrEventAt(time.Hour, "503", "QueueFull"),    // exactly the window end
		}
		imp := seedTracingImport(t, e.store, id, "iis-httperr", "a.log", evs)
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		l := tracingLaneOf(t, out, "iis")
		if len(l.Events) != 0 {
			t.Errorf("events %d, want 0: %+v", len(l.Events), l.Events)
		}
		if got := tracingBucketSum(l); got != 0 || l.Scanned != 0 {
			t.Errorf("bucket sum %d scanned %d, want 0", got, l.Scanned)
		}
	})
}

// TestHTTPERRLabel is the table of the label rule's own examples, plus the
// empty and too-long-even-alone cases. Every result must be at most 40 runes
// and pass evidence.LintCausal for every canonical reason.
func TestHTTPERRLabel(t *testing.T) {
	tests := []struct {
		reason, want string
	}{
		{"", "HTTP.sys error"},
		{"Connection_Dropped", "HTTP.sys error: Connection_Dropped"},
		{"Connection_Abandoned_By_ReqQueue", "Connection_Abandoned_By_ReqQueue"},
		{strings.Repeat("x", 45), "HTTP.sys error"},
	}
	for _, tt := range tests {
		got := httperrLabel(tt.reason)
		if got != tt.want {
			t.Errorf("httperrLabel(%q) = %q, want %q", tt.reason, got, tt.want)
		}
		if n := len([]rune(got)); n > 40 {
			t.Errorf("httperrLabel(%q) = %q, %d runes, want <= 40", tt.reason, got, n)
		}
	}
	for _, canon := range []string{
		"AppOffline", "AppPoolTimer", "AppShutdown", "BadRequest", "Client_Reset",
		"Connection_Abandoned_By_AppPool", "Connection_Abandoned_By_ReqQueue",
		"Connection_Dropped", "ConnLimit", "Disabled", "EntityTooLarge", "FieldLength",
		"Forbidden", "Header", "Hostname", "Internal", "N/A", "Number", "Precondition",
		"QueueFull", "RequestLength", "Timer_AppPool", "Timer_ConnectionIdle",
		"Timer_EntityBody", "Timer_HeaderWait", "Timer_MinBytesPerSecond", "URL",
		"UrlLength", "Verb", "Version_N/P", "other",
	} {
		label := httperrLabel(canon)
		if n := len([]rune(label)); n > 40 {
			t.Errorf("httperrLabel(%q) = %q, %d runes, want <= 40", canon, label, n)
		}
		if err := evidence.LintCausal(label); err != nil {
			t.Errorf("LintCausal(httperrLabel(%q)) = %v, want nil", canon, err)
		}
	}
}

// TestTracingHTTPERRNoExtraAttrs proves the raw body never carries stem,
// queue or site id values, only the allowlisted reason.
func TestTracingHTTPERRNoExtraAttrs(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		at := tracingLaneT0.Add(30 * time.Minute)
		ev := evidence.Event{Kind: "httperr", Host: "web-a", Start: at, End: at, TZRule: clock.RuleUTCField,
			Attrs: map[string]string{
				"status": "503", "reason": "QueueFull",
				"stem": "CANARYSTEM", "queue": "CANARYQUEUE", "siteid": "CANARYSITEID",
			}}
		imp := seedTracingImport(t, e.store, id, "iis-httperr", "a.log", []evidence.Event{ev})
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		whole, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{raw, string(whole)} {
			for _, banned := range []string{"CANARYSTEM", "CANARYQUEUE", "CANARYSITEID"} {
				if strings.Contains(body, banned) {
					t.Errorf("response holds %q: %s", banned, body)
				}
			}
		}
	})
}

// TestTracingHTTPERRUnknownReasonHidden proves a stored reason outside the
// importer's allowlist is never shown.
func TestTracingHTTPERRUnknownReasonHidden(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-httperr", "a.log",
			[]evidence.Event{httperrEventAt(30*time.Minute, "503", "SomethingTheImporterNeverEmits")})
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		l := tracingLaneOf(t, out, "iis")
		if len(l.Events) != 1 {
			t.Fatalf("events %d, want 1: %+v", len(l.Events), l.Events)
		}
		if got := l.Events[0].Label; got != "HTTP.sys error" {
			t.Errorf("label %q, want %q", got, "HTTP.sys error")
		}
		if strings.Contains(raw, "SomethingTheImporterNeverEmits") {
			t.Errorf("response holds the unknown reason: %s", raw)
		}
	})
}
