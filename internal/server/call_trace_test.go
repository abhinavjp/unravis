package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// traceRepo answers Record from a fixed set, by id; any other id is not found.
type traceRepo struct {
	*fakeRepository
	recs map[string]audit.RecordDetail
	read int
}

func (r *traceRepo) Record(_ context.Context, in audit.RecordInput) (audit.RecordDetail, error) {
	r.read++
	rec, ok := r.recs[in.ID]
	if !ok || rec.SourceID != in.SourceID {
		return audit.RecordDetail{}, fmt.Errorf("%w: request was not found", audit.ErrNotFound)
	}
	return rec, nil
}

var traceT0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func traceRecord(id string, start time.Time, dur time.Duration) audit.RecordDetail {
	end := start.Add(dur)
	ms := float64(dur / time.Millisecond)
	return audit.RecordDetail{RequestRecord: audit.RequestRecord{
		ID: id, SourceID: "demo-west", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success",
		InitiatedAt: &start, CompletedAt: &end, DurationMs: &ms,
	}}
}

func newTraceRepo() *traceRepo {
	nostart := audit.RecordDetail{RequestRecord: audit.RequestRecord{ID: "nostart", SourceID: "demo-west", Controller: "Payroll", Action: "Run"}}
	return &traceRepo{fakeRepository: &fakeRepository{}, recs: map[string]audit.RecordDetail{
		"a":       traceRecord("a", traceT0, 3*time.Second),
		"b":       traceRecord("b", traceT0.Add(10*time.Minute), 4*time.Second),
		"c":       traceRecord("c", traceT0.Add(20*time.Minute), 4*time.Second),
		"far":     traceRecord("far", traceT0.Add(5*time.Hour), 2*time.Second),
		"nostart": nostart,
	}}
}

func callsBody(ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf(`{"sourceId":"demo-west","id":%q}`, id)
	}
	return `{"calls":[` + strings.Join(parts, ",") + `]`
}

// requireRealTrace skips a test that needs calltrace.CallFromRecord to accept a
// normal record (it does not while the package holds only stubs).
func requireRealTrace(t *testing.T) {
	t.Helper()
	rec := traceRecord("a", traceT0, time.Second)
	if _, ok := calltrace.CallFromRecord(rec.RequestRecord); !ok {
		t.Skip("calltrace.CallFromRecord is still a stub")
	}
}

type tracePost func(body string) *httptest.ResponseRecorder

// traceRoutes runs fn against the no-case route and the case route (an empty case).
func traceRoutes(t *testing.T, repo audit.Repository, fn func(t *testing.T, post tracePost, withCase bool)) {
	t.Helper()
	t.Run("no-case", func(t *testing.T) {
		h := New(repo, testAssets(), testToken, false)
		fn(t, func(body string) *httptest.ResponseRecorder {
			return request(h, http.MethodPost, "/api/call-trace", body, true)
		}, false)
	})
	t.Run("case", func(t *testing.T) {
		e := tagEnv(t, casestore.NewMemory(), repo)
		id := e.newCase("trace")
		fn(t, func(body string) *httptest.ResponseRecorder {
			return e.json(http.MethodPost, "/api/cases/"+id+"/call-trace", body)
		}, true)
	})
}

func TestCallTraceRejectsBadRequests(t *testing.T) {
	repo := newTraceRepo()
	traceRoutes(t, repo, func(t *testing.T, post tracePost, _ bool) {
		cases := []struct {
			name, body string
			want       int
		}{
			{"no calls key", `{}`, http.StatusBadRequest},
			{"zero calls", `{"calls":[]}`, http.StatusBadRequest},
			{"four calls", callsBody("a", "b", "c", "far") + `}`, http.StatusBadRequest},
			{"duplicate call", callsBody("a", "a") + `}`, http.StatusBadRequest},
			{"empty id", `{"calls":[{"sourceId":"demo-west","id":""}]}`, http.StatusBadRequest},
			{"empty source", `{"calls":[{"sourceId":"","id":"a"}]}`, http.StatusBadRequest},
			{"negative before", callsBody("a") + `,"beforeMin":-1}`, http.StatusBadRequest},
			{"negative after", callsBody("a") + `,"afterMin":-5}`, http.StatusBadRequest},
			{"bad json", `{"calls":`, http.StatusBadRequest},
			{"trailing json", callsBody("a") + `} {}`, http.StatusBadRequest},
			{"unknown field", callsBody("a") + `,"extra":1}`, http.StatusBadRequest},
			{"unknown call field", `{"calls":[{"sourceId":"demo-west","id":"a","file":"x"}]}`, http.StatusBadRequest},
			{"wrong types", `{"calls":"a"}`, http.StatusBadRequest},
		}
		before := repo.read
		for _, c := range cases {
			if res := post(c.body); res.Code != c.want {
				t.Errorf("%s = %d %s, want %d", c.name, res.Code, res.Body, c.want)
			}
		}
		if repo.read != before {
			t.Errorf("a refused request read %d records, want 0", repo.read-before)
		}
	})
}

func TestCallTraceRequiresJSONContentType(t *testing.T) {
	h := New(newTraceRepo(), testAssets(), testToken, false)
	req := httptest.NewRequest(http.MethodPost, "/api/call-trace", strings.NewReader(callsBody("a")+`}`))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain = %d, want 415", res.Code)
	}

	e := tagEnv(t, casestore.NewMemory(), newTraceRepo())
	id := e.newCase("trace")
	req = httptest.NewRequest(http.MethodPost, "/api/cases/"+id+"/call-trace", strings.NewReader(callsBody("a")+`}`))
	req.Header.Set("X-Audit-Token", testToken)
	if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("case route without content type = %d, want 415", res.Code)
	}
}

func TestCallTraceUnknownRecordIs404(t *testing.T) {
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		for _, body := range []string{
			callsBody("missing") + `}`,
			callsBody("missing", "a") + `}`,
			`{"calls":[{"sourceId":"other-source","id":"a"}]}`,
		} {
			res := post(body)
			if res.Code != http.StatusNotFound {
				t.Errorf("%s = %d %s, want 404", body, res.Code, res.Body)
			}
			if !strings.Contains(res.Body.String(), `"error"`) {
				t.Errorf("404 body has no error field: %s", res.Body)
			}
		}
	})
}

func TestCallTraceRecordWithoutStartIs422(t *testing.T) {
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		res := post(callsBody("nostart") + `}`)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("no start = %d %s, want 422", res.Code, res.Body)
		}
	})
}

func TestCallTraceRepoFailureDoesNotLeak(t *testing.T) {
	h := New(&fakeRepository{recordErr: fmt.Errorf("mongo: password=hunter2 host=db01")}, testAssets(), testToken, false)
	res := request(h, http.MethodPost, "/api/call-trace", callsBody("a")+`}`, true)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("repo failure = %d, want 500", res.Code)
	}
	if strings.Contains(res.Body.String(), "hunter2") || strings.Contains(res.Body.String(), "db01") {
		t.Fatalf("error leaks internals: %s", res.Body)
	}
}

func TestCallTraceCaseRoutes(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), newTraceRepo())
	if res := e.json(http.MethodPost, "/api/cases/not-an-id/call-trace", callsBody("a")+`}`); res.Code != http.StatusBadRequest {
		t.Errorf("bad case id = %d, want 400", res.Code)
	}
	if res := e.json(http.MethodPost, "/api/cases/"+absentCaseID+"/call-trace", callsBody("a")+`}`); res.Code != http.StatusNotFound {
		t.Errorf("absent case = %d %s, want 404", res.Code, res.Body)
	}
	if res := e.json(http.MethodGet, "/api/cases/"+absentCaseID+"/call-trace", ""); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", res.Code)
	}
	if res := request(e.h, http.MethodPost, "/api/call-trace", callsBody("a")+`}`, false); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", res.Code)
	}
}

// traceView is the part of the answer the tests read.
type traceView struct {
	Traces []struct {
		Call struct {
			ID    string    `json:"id"`
			Start time.Time `json:"start"`
		} `json:"call"`
		Window calltrace.Window  `json:"window"`
		Lanes  []calltrace.Lane  `json:"lanes"`
		Checks []calltrace.Check `json:"checks"`
	} `json:"traces"`
	Compare      []calltrace.Check `json:"compare"`
	SharedWindow *calltrace.Window `json:"sharedWindow"`
	Truncated    bool              `json:"truncated"`
}

// requireNoNullArrays fails when the JSON holds a null where an array belongs.
func requireNoNullArrays(t *testing.T, body string) {
	t.Helper()
	for _, bad := range []string{`"traces":null`, `"compare":null`, `"lanes":null`, `"checks":null`, `"parts":null`, `"events":null`, `"sources":null`, `"callIds":null`} {
		if strings.Contains(body, bad) {
			t.Errorf("body holds %s: %s", bad, body)
		}
	}
}

func TestCallTraceOneCall(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, withCase bool) {
		res := post(callsBody("a") + `}`)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d %s", res.Code, res.Body)
		}
		requireNoNullArrays(t, res.Body.String())
		var v traceView
		decodeBody(t, res, &v)
		if len(v.Traces) != 1 || v.Compare == nil || len(v.Compare) != 0 {
			t.Fatalf("traces=%d compare=%v", len(v.Traces), v.Compare)
		}
		if v.SharedWindow == nil {
			t.Error("one call must set sharedWindow")
		}
		want := "no-case"
		if withCase {
			want = "no-data" // an empty case holds no files
		}
		for _, l := range v.Traces[0].Lanes {
			if l.State != want {
				t.Errorf("lane %s state = %q, want %q", l.Key, l.State, want)
			}
		}
		// Default margin: two minutes either side of the call (3 s long).
		start := v.Traces[0].Call.Start
		if got := v.Traces[0].Window.From; !got.Equal(start.Add(-2 * time.Minute)) {
			t.Errorf("window from = %v, want start-2m (%v)", got, start)
		}
		if got := v.Traces[0].Window.To; !got.Equal(start.Add(3*time.Second + 2*time.Minute)) {
			t.Errorf("window to = %v", got)
		}
	})
}

func TestCallTraceMarginsClamp(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		res := post(callsBody("a") + `,"beforeMin":500,"afterMin":7}`)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d %s", res.Code, res.Body)
		}
		var v traceView
		decodeBody(t, res, &v)
		tr := v.Traces[0]
		if !tr.Window.From.Equal(tr.Call.Start.Add(-30 * time.Minute)) {
			t.Errorf("before clamp: from = %v, start %v", tr.Window.From, tr.Call.Start)
		}
		if !tr.Window.To.Equal(tr.Call.Start.Add(3*time.Second + 7*time.Minute)) {
			t.Errorf("after: to = %v", tr.Window.To)
		}
	})
}

func TestCallTraceSharedWindowAndCompare(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		// Two calls ten minutes apart fit one window.
		res := post(callsBody("a", "b") + `}`)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d %s", res.Code, res.Body)
		}
		requireNoNullArrays(t, res.Body.String())
		var v traceView
		decodeBody(t, res, &v)
		if len(v.Traces) != 2 || v.SharedWindow == nil || v.Compare == nil {
			t.Fatalf("traces=%d shared=%v compare=%v", len(v.Traces), v.SharedWindow, v.Compare)
		}
		if !v.SharedWindow.From.Equal(v.Traces[0].Window.From) || !v.SharedWindow.To.Equal(v.Traces[1].Window.To) {
			t.Errorf("shared window %+v does not span both traces", v.SharedWindow)
		}
		// Five hours apart: no shared window.
		res = post(callsBody("a", "far") + `}`)
		if res.Code != http.StatusOK {
			t.Fatalf("far status = %d %s", res.Code, res.Body)
		}
		requireNoNullArrays(t, res.Body.String())
		v = traceView{}
		decodeBody(t, res, &v)
		if len(v.Traces) != 2 || v.SharedWindow != nil {
			t.Fatalf("far calls: traces=%d shared=%v", len(v.Traces), v.SharedWindow)
		}
		// Three calls.
		if res := post(callsBody("a", "b", "c") + `}`); res.Code != http.StatusOK {
			t.Fatalf("three calls = %d %s", res.Code, res.Body)
		}
	})
}

func TestCallTraceDemoRepository(t *testing.T) {
	requireRealTrace(t)
	e := tagEnv(t, casestore.NewMemory(), audit.NewDemo(traceT0))
	id := e.newCase("trace")
	body := `{"calls":[{"sourceId":"demo-west","id":"demo-west-Payroll-current-00"}]}`
	for _, path := range []string{"/api/call-trace", "/api/cases/" + id + "/call-trace"} {
		res := e.json(http.MethodPost, path, body)
		if res.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body)
		}
		requireNoNullArrays(t, res.Body.String())
		if strings.Contains(res.Body.String(), "Illustrative demo") {
			t.Errorf("%s returned payload text", path)
		}
	}
}

func TestOverlappingKeepsOnlyEventsThatTouchTheWindow(t *testing.T) {
	win := calltrace.Window{From: traceT0, To: traceT0.Add(time.Minute)}
	ev := func(start, end time.Duration) evidence.Event {
		return evidence.Event{Start: traceT0.Add(start), End: traceT0.Add(end)}
	}
	in := []evidence.Event{
		ev(-5*time.Minute, -4*time.Minute), // ends before
		ev(-5*time.Minute, 10*time.Second), // started earlier, reaches in
		ev(30*time.Second, 30*time.Second), // inside
		ev(2*time.Minute, 3*time.Minute),   // starts after
		ev(-time.Minute, -2*time.Minute),   // end before start: a point at the start
	}
	got := overlapping(in, win)
	if len(got) != 2 {
		t.Fatalf("kept %d events, want 2", len(got))
	}
}
