package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/investigate"
)

var invT0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func invRow(i int, start time.Time, dur time.Duration, c float64) audit.RequestRecord {
	end := start.Add(dur)
	a, p := 40.0, 5.0
	uid := 1 + i%7
	return audit.RequestRecord{
		ID: fmt.Sprintf("r%05d", i), SourceID: "demo-west", Controller: "Payroll", Action: "Run",
		Kind: "synchronous", Outcome: "success", URL: "/api/payroll/run",
		InitiatedAt: &start, CompletedAt: &end, AuditCreateTimeMs: &a, ApiTimeMs: &c, PayloadRecordTimeMs: &p, UserID: &uid,
	}
}

// invRows is a quiet hour (09:00) then a slow ninety minutes (10:00), 6 calls a minute.
func invRows() []audit.RequestRecord {
	var rows []audit.RequestRecord
	n := 0
	for m := 0; m < 150; m++ {
		for k := 0; k < 6; k++ {
			start := invT0.Add(time.Duration(m)*time.Minute + time.Duration(k*10)*time.Second)
			if m < 60 {
				rows = append(rows, invRow(n, start, 300*time.Millisecond, 250))
			} else {
				rows = append(rows, invRow(n, start, 2500*time.Millisecond, 2450))
			}
			n++
		}
	}
	return rows
}

const invBody = `{"sourceIds":["demo-west"],
 "incident":{"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:30:00Z"},
 "reference":{"start":"2026-10-20T09:00:00Z","end":"2026-10-20T10:00:00Z"}%s}`

func invPost(extra string) string {
	return fmt.Sprintf(invBody, extra)
}

func okPage(rows []audit.RequestRecord) audit.Page {
	return audit.Page{Rows: rows, Sources: []audit.SourceResult{{SourceID: "demo-west", OK: true}}}
}

func TestInvestigationRouteAnswers(t *testing.T) {
	repo := &fakeRepository{requestPages: []audit.Page{okPage(invRows())}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(`,"poolLimit":50,"releasesEntered":true`))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if out.Result.Version != investigate.Version || len(out.Result.Steps) != investigate.StepCount {
		t.Fatalf("version %q steps %d", out.Result.Version, len(out.Result.Steps))
	}
	if out.Read.Requests != 900 || out.Read.Truncated {
		t.Fatalf("read = %+v", out.Read)
	}
	if out.Result.Facts.Series != nil {
		t.Errorf("series sent without being asked for")
	}
	if out.Result.Facts.Onset.Status != "found" {
		t.Errorf("onset = %+v", out.Result.Facts.Onset)
	}
	if got := out.Result.Steps[3].Readiness; got != investigate.PartlyReady {
		t.Errorf("step 3 = %q, want partly ready without IIS", got)
	}
	// With the series asked for, the per-minute rows come too.
	res = e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(`,"includeSeries":true`))
	var withSeries investigationAnswer
	decodeBody(t, res, &withSeries)
	if len(withSeries.Result.Facts.Series) == 0 {
		t.Errorf("no series although asked for")
	}
}

func TestInvestigationRouteRefusesBadBodies(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("inv")
	path := "/api/cases/" + id + "/investigation"
	bad := map[string]string{
		"not json":           `{`,
		"unknown field":      invPost(`,"bogus":1`),
		"pool limit zero":    invPost(`,"poolLimit":0`),
		"pool limit huge":    invPost(`,"poolLimit":99999999`),
		"no sources":         strings.Replace(invPost(""), `["demo-west"]`, `[]`, 1),
		"incident reversed":  strings.Replace(invPost(""), `"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:30:00Z"`, `"start":"2026-10-20T11:30:00Z","end":"2026-10-20T10:00:00Z"`, 1),
		"window missing":     `{"sourceIds":["a"]}`,
		"span over a week":   strings.Replace(invPost(""), `2026-10-20T09:00:00Z`, `2026-09-01T09:00:00Z`, 1),
		"release no time":    invPost(`,"releases":[{"label":"x"}]`),
		"release label long": invPost(`,"releases":[{"at":"2026-10-20T09:50:00Z","label":"` + strings.Repeat("x", 300) + `"}]`),
	}
	for name, body := range bad {
		if res := e.json(http.MethodPost, path, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	if res := e.get(path); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", res.Code)
	}
	if res := request(e.h, http.MethodPost, path, invPost(""), false); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", res.Code)
	}
	if res := e.json(http.MethodPost, "/api/cases/nope-not-a-case/investigation", invPost("")); res.Code == http.StatusOK {
		t.Errorf("unknown case answered 200")
	}
}

func TestInvestigationEmptyAuditIsNotAnAnswer(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if out.Result.Confidence.Word != investigate.ConfidenceNone {
		t.Errorf("confidence = %q", out.Result.Confidence.Word)
	}
	for _, s := range out.Result.Suspects {
		if s.Colour == investigate.Red || s.Colour == investigate.Orange {
			t.Errorf("%s is %s on an empty audit", s.ID, s.Colour)
		}
	}
}

func TestInvestigationTooManyCallsFailsTheAuditNotTheRun(t *testing.T) {
	// The fake keeps returning a page with a next cursor, so the read hits the cap.
	page := okPage(invRows())
	page.NextCursor = "more"
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{page}})
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if !out.Read.Truncated || len(out.Read.Warnings) == 0 || out.Read.Requests != 0 {
		t.Errorf("read = %+v", out.Read)
	}
	if out.Result.Steps[0].Readiness != investigate.CannotTestYet && out.Result.Steps[0].Readiness != investigate.Failed {
		t.Errorf("step 0 = %q after a truncated read", out.Result.Steps[0].Readiness)
	}
}

func TestInvestigationFailedSourceIsReported(t *testing.T) {
	page := okPage(invRows())
	page.Sources = []audit.SourceResult{{SourceID: "demo-west", OK: false, Error: "boom"}}
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{page}})
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	var out investigationAnswer
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || len(out.Read.Warnings) == 0 {
		t.Fatalf("code %d read %+v", res.Code, out.Read)
	}
	if strings.Contains(res.Body.String(), "boom") {
		t.Errorf("source error text reached the answer")
	}
}

func iisEvent(i int, start time.Time, takenMs float64) evidence.Event {
	d := takenMs
	return evidence.Event{
		EvidenceID: fmt.Sprintf("e%d", i), Source: investigationIISSource, Kind: "request",
		Start: start, End: start.Add(time.Duration(takenMs) * time.Millisecond), DurationMs: &d,
		Attrs: map[string]string{"stem": "/API/Payroll/Run/"},
	}
}

func TestInvestigationIISRowsAreReadFromTheCase(t *testing.T) {
	body := investigationRequest{SourceIDs: []string{"demo-west"},
		Incident:  investigationWindow{invT0.Add(time.Hour), invT0.Add(150 * time.Minute)},
		Reference: investigationWindow{invT0, invT0.Add(time.Hour)}}
	c := &stubTraceCase{
		imports: []casestore.ImportRecord{stubImport(investigationIISSource, invT0, invT0.Add(3*time.Hour))},
		events: func(q casestore.EventQuery) []evidence.Event {
			if len(q.Sources) != 1 || q.Sources[0] != investigationIISSource || len(q.Kinds) != 1 || q.Kinds[0] != "request" {
				t.Errorf("query = %+v", q)
			}
			return []evidence.Event{iisEvent(1, invT0.Add(time.Minute), 120), iisEvent(2, invT0.Add(2*time.Minute), 130)}
		},
	}
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, body, nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	if !in.IISPresent || len(in.IIS) != 2 || read.IISRows != 2 {
		t.Fatalf("IIS = %v rows %d read %+v", in.IISPresent, len(in.IIS), read)
	}
	if in.IIS[0].Path != "/api/payroll/run" || in.IIS[0].TakenMs != 120 {
		t.Errorf("row = %+v", in.IIS[0])
	}

	// No IIS importer in the case: IIS is absent, not empty.
	none := &stubTraceCase{imports: []casestore.ImportRecord{stubImport("sqlserver-xe", invT0, invT0.Add(time.Hour))}}
	read = investigationRead{}
	in, _ = buildInvestigationInput(context.Background(), none, body, nil, investigate.AuditPresent, &read)
	if in.IISPresent || len(none.queries) != 0 {
		t.Errorf("IIS present without an IIS file; queries %d", len(none.queries))
	}

	// An IIS read that hit the cap is not used, and says so.
	capped := &stubTraceCase{
		imports: c.imports,
		events: func(casestore.EventQuery) []evidence.Event {
			return make([]evidence.Event, casestore.MaxEventQuery)
		},
	}
	read = investigationRead{}
	in, _ = buildInvestigationInput(context.Background(), capped, body, nil, investigate.AuditPresent, &read)
	if in.IISPresent || len(read.Warnings) == 0 {
		t.Errorf("capped IIS read was used: %v %+v", in.IISPresent, read)
	}
}

// windowRepo answers each audit query with the calls of that query's own window
// (one call a second on one route) and records the windows it was asked for.
type windowRepo struct {
	*fakeRepository
	asked []audit.Query
}

func (w *windowRepo) Requests(_ context.Context, in audit.PageInput) (audit.Page, error) {
	w.asked = append(w.asked, in.Query)
	var rows []audit.RequestRecord
	n := 0
	for t := in.Query.Start; t.Before(in.Query.End); t = t.Add(time.Second) {
		rows = append(rows, invRow(n, t, 300*time.Millisecond, 250))
		n++
	}
	return okPage(rows), nil
}

// F4: the two windows are read on their own. A reference a week or a day earlier
// must not make the span a 400 or push the read over the row cap.
func TestInvestigationReadsEachWindowNotTheSpanBetween(t *testing.T) {
	cases := map[string]string{
		"a week earlier": `2026-10-13T10:00:00Z","end":"2026-10-13T11:00:00Z`,
		"a day earlier":  `2026-10-19T10:00:00Z","end":"2026-10-19T11:00:00Z`,
	}
	for name, ref := range cases {
		repo := &windowRepo{fakeRepository: &fakeRepository{}}
		e := tagEnv(t, casestore.NewMemory(), repo)
		id := e.newCase("inv")
		body := `{"sourceIds":["demo-west"],
 "incident":{"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z"},
 "reference":{"start":"` + ref + `"}}`
		res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", body)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: code = %d %s", name, res.Code, res.Body)
		}
		var out investigationAnswer
		decodeBody(t, res, &out)
		if out.Read.Truncated || out.Read.Requests != 7200 || out.Result.Facts.Counts.Used == 0 {
			t.Errorf("%s: read = %+v", name, out.Read)
		}
		if out.Result.Steps[0].Readiness == investigate.Failed || out.Result.Facts.Counts.Used != 7200 {
			t.Errorf("%s: audit failed or partly used: used %d", name, out.Result.Facts.Counts.Used)
		}
		if len(repo.asked) != 2 {
			t.Fatalf("%s: %d audit queries, want one per window", name, len(repo.asked))
		}
		for _, q := range repo.asked {
			if q.End.Sub(q.Start) != time.Hour {
				t.Errorf("%s: asked for %v to %v", name, q.Start, q.End)
			}
		}
	}
}

func TestInvestigationRangesAreEachReadOnce(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	win := func(a, b time.Time) investigationWindow { return investigationWindow{a, b} }
	cases := []struct {
		name     string
		ref, inc investigationWindow
		want     []investigationWindow
	}{
		{"apart", win(at(13, 10), at(13, 11)), win(at(20, 10), at(20, 11)), []investigationWindow{win(at(13, 10), at(13, 11)), win(at(20, 10), at(20, 11))}},
		{"touching", win(at(20, 9), at(20, 10)), win(at(20, 10), at(20, 11)), []investigationWindow{win(at(20, 9), at(20, 11))}},
		{"overlapping", win(at(20, 9), at(20, 11)), win(at(20, 10), at(20, 12)), []investigationWindow{win(at(20, 9), at(20, 12))}},
		{"incident first", win(at(20, 12), at(20, 13)), win(at(20, 10), at(20, 11)), []investigationWindow{win(at(20, 10), at(20, 11)), win(at(20, 12), at(20, 13))}},
		{"inside", win(at(20, 10), at(20, 11)), win(at(20, 10), at(20, 11)), []investigationWindow{win(at(20, 10), at(20, 11))}},
	}
	for _, c := range cases {
		got := investigationRequest{Incident: c.inc, Reference: c.ref}.ranges()
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if !got[i].Start.Equal(c.want[i].Start) || !got[i].End.Equal(c.want[i].End) {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// F4: IIS is read per window too, each with its own cap, and an event held by both
// reads is counted once.
func TestInvestigationIISIsReadPerWindow(t *testing.T) {
	body := investigationRequest{SourceIDs: []string{"demo-west"},
		Incident:  investigationWindow{invT0.Add(24 * time.Hour), invT0.Add(25 * time.Hour)},
		Reference: investigationWindow{invT0, invT0.Add(time.Hour)}}
	c := &stubTraceCase{
		imports: []casestore.ImportRecord{stubImport(investigationIISSource, invT0, invT0.Add(30*time.Hour))},
		events: func(q casestore.EventQuery) []evidence.Event {
			if q.Window.To.Sub(q.Window.From) > 2*time.Hour {
				t.Errorf("IIS read %v to %v spans the gap", q.Window.From, q.Window.To)
			}
			return []evidence.Event{iisEvent(1, invT0.Add(time.Minute), 120)}
		},
	}
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, body, nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.queries) != 2 || !in.IISPresent || len(in.IIS) != 1 {
		t.Errorf("queries %d present %v rows %d", len(c.queries), in.IISPresent, len(in.IIS))
	}
}
