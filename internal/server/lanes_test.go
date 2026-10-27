package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
	"api-audit-explorer/internal/sqlconn"
	"api-audit-explorer/internal/store"
)

func lday(day, h int) time.Time { return time.Date(2026, 10, day, h, 0, 0, 0, time.UTC) }

// laneQ answers each lane query by the marker comment in its text.
type laneQ struct {
	by  map[string][]dbhealth.Row
	err map[string]error
}

func (f laneQ) Query(_ context.Context, text string) ([]dbhealth.Row, error) {
	for k, e := range f.err {
		if strings.Contains(text, k) {
			return nil, e
		}
	}
	for k, r := range f.by {
		if strings.Contains(text, k) {
			return r, nil
		}
	}
	return nil, nil
}
func (laneQ) Close() {}

func healthyLaneSQL() laneQ {
	qs := func(q, p int64, win string, execs int64, ms, reads float64) dbhealth.Row {
		return dbhealth.Row{"query_id": q, "plan_id": p, "win": win, "execs": execs, "avg_ms": ms, "avg_reads": reads}
	}
	return laneQ{by: map[string][]dbhealth.Row{
		"unravis:base":       {{"version": "16.0.1000.6", "edition": "Standard", "hadr": int64(0), "utc_offset_min": int64(0)}},
		"unravis:server":     {{"started_local": lday(1, 3), "threadpool_tasks": int64(0)}},
		"unravis:qs_options": {{"actual_state_desc": "READ_WRITE", "desired_state_desc": "READ_WRITE", "readonly_reason": int64(0), "interval_length_minutes": int64(15)}},
		"unravis:qs_first":   {{"first_start": lday(2, 0)}},
		"unravis:qs_stats":   {qs(1, 10, "ref", 50, 10, 100), qs(1, 10, "inc", 50, 11, 105)},
	}}
}

func stubLaneSQL(t *testing.T, q laneQ) *[]dbhealth.SQLTarget {
	t.Helper()
	var seen []dbhealth.SQLTarget
	old := openSQLFn
	t.Cleanup(func() { openSQLFn = old })
	openSQLFn = func(_ context.Context, tg dbhealth.SQLTarget) (healthQueryer, error) {
		seen = append(seen, tg)
		return q, nil
	}
	return &seen
}

const laneWindowsJSON = `"incident":{"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z"},"reference":{"start":"2026-10-19T10:00:00Z","end":"2026-10-19T11:00:00Z"}`

func laneSQLBodyJSON(extra string) string {
	return `{"savedId":"saved1",` + extra + laneWindowsJSON + `}`
}

func savedLane() *fakeSavedSQL {
	return &fakeSavedSQL{in: sqlconn.Input{Label: "Prod SQL", Server: "db01.corp.example", Auth: "sql", Username: "unravis_ro", Password: "stored-pw", Database: "AuditDb", Encrypt: "mandatory"}}
}

type laneRunOut struct {
	Saved bool         `json:"saved"`
	State string       `json:"state"`
	Run   healthRunDTO `json:"run"`
}

func TestLaneSQLRunsSavesAndLists(t *testing.T) {
	seen := stubLaneSQL(t, healthyLaneSQL())
	healthEnv(t, savedLane(), func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", laneSQLBodyJSON(""))
		if res.Code != http.StatusOK {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
		var out laneRunOut
		decodeBody(t, res, &out)
		if !out.Saved || out.State != "Ready" || out.Run.Kind != "sql" || out.Run.Label != "Prod SQL" {
			t.Fatalf("out = %+v", out)
		}
		for _, secret := range []string{"stored-pw", "db01.corp.example", "unravis_ro\":"} {
			if strings.Contains(res.Body.String(), secret) {
				t.Fatalf("%q leaked: %s", secret, res.Body)
			}
		}
		// The same day again is a re-check: allowed, and the list still shows one day.
		if res = e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", laneSQLBodyJSON("")); res.Code != http.StatusOK {
			t.Fatalf("re-check = %d %s", res.Code, res.Body)
		}
		var list struct{ Days []laneDayDTO }
		decodeBody(t, e.get("/api/cases/"+id+"/lanes"), &list)
		if len(list.Days) != 1 || list.Days[0].State != "Ready" || list.Days[0].Lane.Answers["D5"] != "no" || list.Days[0].Database != "AuditDb" {
			t.Fatalf("days = %+v", list.Days)
		}
		// A different day is added next to it.
		next := `{"savedId":"saved1","incident":{"start":"2026-10-22T10:00:00Z","end":"2026-10-22T11:00:00Z"},"reference":{"start":"2026-10-21T10:00:00Z","end":"2026-10-21T11:00:00Z"}}`
		if res = e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", next); res.Code != http.StatusOK {
			t.Fatalf("second day = %d %s", res.Code, res.Body)
		}
		decodeBody(t, e.get("/api/cases/"+id+"/lanes"), &list)
		if len(list.Days) != 2 {
			t.Fatalf("days = %d", len(list.Days))
		}
		// The ordinary health list still holds the runs, with the lane inside the report.
		var runs struct{ Runs []healthRunDTO }
		decodeBody(t, e.get("/api/cases/"+id+"/health"), &runs)
		if len(runs.Runs) != 3 {
			t.Fatalf("health runs = %d", len(runs.Runs))
		}
	})
	if len(*seen) == 0 || (*seen)[0].Password != "stored-pw" || (*seen)[0].Database != "AuditDb" {
		t.Fatalf("login = %+v", *seen)
	}
}

func TestLaneSQLTwoOverlappingDaysAreRefused(t *testing.T) {
	seen := stubLaneSQL(t, healthyLaneSQL())
	healthEnv(t, savedLane(), func(t *testing.T, e *caseEnv, id string) {
		path := "/api/cases/" + id + "/lanes/sql"
		if res := e.json(http.MethodPost, path, laneSQLBodyJSON("")); res.Code != http.StatusOK {
			t.Fatalf("first = %d %s", res.Code, res.Body)
		}
		before := len(*seen)
		overlap := `{"savedId":"saved1","incident":{"start":"2026-10-20T10:30:00Z","end":"2026-10-20T12:00:00Z"},"reference":{"start":"2026-10-19T10:00:00Z","end":"2026-10-19T11:00:00Z"}}`
		res := e.json(http.MethodPost, path, overlap)
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "overlaps") || !strings.Contains(res.Body.String(), "Nothing was read") {
			t.Fatalf("overlap = %d %s", res.Code, res.Body)
		}
		if len(*seen) != before {
			t.Error("the server was read for a day that was refused")
		}
		// A different database is a different source: no conflict.
		other := `{"savedId":"saved1","database":"OtherDb","incident":{"start":"2026-10-20T10:30:00Z","end":"2026-10-20T12:00:00Z"},"reference":{"start":"2026-10-19T10:00:00Z","end":"2026-10-19T11:00:00Z"}}`
		if res = e.json(http.MethodPost, path, other); res.Code != http.StatusOK {
			t.Fatalf("other database = %d %s", res.Code, res.Body)
		}
	})
}

func TestLaneSQLWrongPasswordGivesPlainWordsAndKeepsNothing(t *testing.T) {
	old := openSQLFn
	t.Cleanup(func() { openSQLFn = old })
	openSQLFn = func(context.Context, dbhealth.SQLTarget) (healthQueryer, error) {
		return nil, errors.New("mssql: Login failed for user 'unravis_ro' on PRODSQL01")
	}
	healthEnv(t, savedLane(), func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", laneSQLBodyJSON(`"password":"typo-pw",`))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "login_failed") {
			t.Fatalf("wrong password = %d %s", res.Code, res.Body)
		}
		for _, leak := range []string{"PRODSQL01", "typo-pw", "stored-pw"} {
			if strings.Contains(res.Body.String(), leak) {
				t.Fatalf("%q leaked: %s", leak, res.Body)
			}
		}
		var list struct{ Days []laneDayDTO }
		decodeBody(t, e.get("/api/cases/"+id+"/lanes"), &list)
		if list.Days == nil || len(list.Days) != 0 {
			t.Fatalf("a failed sign-in left a day: %+v", list.Days)
		}
	})
}

func TestLaneSQLLoginWithNoGrantsShowsScriptAndAnswersNothing(t *testing.T) {
	deny := errors.New("The SELECT permission was denied on the object 'dm_os_sys_info', database 'mssql_system'.")
	q := laneQ{
		by: map[string][]dbhealth.Row{"unravis:base": {{"version": "15.0.2000.5", "edition": "Standard", "hadr": int64(0), "utc_offset_min": int64(0)}}},
		err: map[string]error{
			"unravis:server": deny, "unravis:errorlog": deny, "unravis:qs_options": deny, "unravis:qs_first": deny, "unravis:qs_stats": deny,
			"unravis:qs_intervals": deny, "unravis:agentjobs": deny, "unravis:backups": deny,
		},
	}
	stubLaneSQL(t, q)
	healthEnv(t, savedLane(), func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", laneSQLBodyJSON(""))
		if res.Code != http.StatusOK {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
		var out laneRunOut
		decodeBody(t, res, &out)
		if out.State != "Cannot test yet" {
			t.Fatalf("state = %q", out.State)
		}
		body := res.Body.String()
		if !strings.Contains(body, "VIEW SERVER STATE") || !strings.Contains(body, "Cannot test yet") {
			t.Fatalf("no script in %s", body)
		}
		if strings.Contains(body, `\"answers\"`) || strings.Contains(body, `"answers"`) {
			t.Fatalf("a login with no grants produced answers: %s", body)
		}
		if strings.Contains(body, "PERFORMANCE STATE") {
			t.Fatalf("2019 server got a 2022 grant: %s", body)
		}
	})
}

func TestLaneRefusals(t *testing.T) {
	stubLaneSQL(t, healthyLaneSQL())
	healthEnv(t, savedLane(), func(t *testing.T, e *caseEnv, id string) {
		path := "/api/cases/" + id + "/lanes/sql"
		for name, body := range map[string]string{
			"no windows":        `{"savedId":"saved1"}`,
			"reference after":   `{"savedId":"saved1","incident":{"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z"},"reference":{"start":"2026-10-20T10:30:00Z","end":"2026-10-20T12:00:00Z"}}`,
			"end before start":  `{"savedId":"saved1","incident":{"start":"2026-10-20T11:00:00Z","end":"2026-10-20T10:00:00Z"},"reference":{"start":"2026-10-19T10:00:00Z","end":"2026-10-19T11:00:00Z"}}`,
			"too long":          `{"savedId":"saved1","incident":{"start":"2026-10-01T10:00:00Z","end":"2026-10-20T11:00:00Z"},"reference":{"start":"2026-09-19T10:00:00Z","end":"2026-09-19T11:00:00Z"}}`,
			"no database":       `{"server":"db01","auth":"windows",` + laneWindowsJSON + `}`,
			"injected database": `{"savedId":"saved1","database":"a;b",` + laneWindowsJSON + `}`,
		} {
			if res := e.json(http.MethodPost, path, body); res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d %s", name, res.Code, res.Body)
			}
		}
		if res := e.json(http.MethodPost, path, `{"savedId":"nope",`+laneWindowsJSON+`}`); res.Code != http.StatusNotFound {
			t.Errorf("unknown saved connection = %d", res.Code)
		}
		if res := e.json(http.MethodPost, "/api/cases/not-a-case/lanes/sql", laneSQLBodyJSON("")); res.Code != http.StatusNotFound {
			t.Errorf("unknown case = %d", res.Code)
		}
		healthBusy.Lock()
		res := e.json(http.MethodPost, path, laneSQLBodyJSON(""))
		healthBusy.Unlock()
		if res.Code != http.StatusConflict {
			t.Errorf("busy = %d %s", res.Code, res.Body)
		}
		if res = e.json(http.MethodGet, path, ""); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET on a run route = %d", res.Code)
		}
	})
}

func TestLaneRoutesAreOffInTheDemo(t *testing.T) {
	stubLaneSQL(t, healthyLaneSQL())
	store := casestore.NewMemory()
	e := tagEnv(t, store, &fakeRepository{}, WithSQLConnections(savedLane()))
	id := e.newCase("demo-ish")
	h := New(&fakeRepository{}, testAssets(), testToken, true, WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})), WithSQLConnections(savedLane()))
	d := &caseEnv{t: t, store: store, h: h}
	for _, p := range []string{"sql", "mongo"} {
		res := d.json(http.MethodPost, "/api/cases/"+id+"/lanes/"+p, laneSQLBodyJSON(""))
		if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "demo") {
			t.Errorf("%s in demo = %d %s", p, res.Code, res.Body)
		}
	}
}

/* ------------------------------------------------------------------- mongo */

type mongoLaneRepo struct {
	*fakeRepository
	run     store.MongoCommandFunc
	openErr error
	opened  int
}

func (m *mongoLaneRepo) OpenCommandClient(_ context.Context, _ audit.ConnectionInput) (store.MongoCommandFunc, func(), error) {
	m.opened++
	if m.openErr != nil {
		return nil, nil, m.openErr
	}
	return m.run, func() {}, nil
}

func mongoDate(t time.Time) map[string]any {
	return map[string]any{"$date": t.UTC().Format(time.RFC3339)}
}

func mongoBatch(docs ...map[string]any) map[string]any {
	arr := make([]any, len(docs))
	for i, d := range docs {
		arr[i] = d
	}
	return map[string]any{"cursor": map[string]any{"firstBatch": arr}}
}

// healthyMongo has a profiler that has run since before the reference window and one whole-collection scan in the incident.
func healthyMongo(cmd string) (map[string]any, error) {
	switch {
	case strings.HasPrefix(cmd, `{"serverStatus"`):
		return map[string]any{"version": "7.0.5", "uptime": float64(86400 * 9)}, nil
	case strings.HasPrefix(cmd, `{"profile"`):
		return map[string]any{"was": float64(1), "slowms": float64(100)}, nil
	case strings.Contains(cmd, `"limit":1,`):
		return mongoBatch(map[string]any{"ts": mongoDate(lday(1, 0))}), nil
	}
	return mongoBatch(
		map[string]any{"ts": mongoDate(lday(20, 10)), "ns": "AuditDb.Logs", "op": "query", "millis": float64(900), "planSummary": "COLLSCAN"},
	), nil
}

func mongoLaneEnv(t *testing.T, repo *mongoLaneRepo, fn func(t *testing.T, e *caseEnv, id string)) {
	t.Helper()
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			st, _ := mk()
			e := tagEnv(t, st, repo)
			fn(t, e, e.newCase("mongo lane"))
		})
	}
}

const laneMongoBodyJSON = `{"uri":"mongodb://mongo01.corp.example","username":"unravis_ro","password":"mongo-secret","database":"AuditDb","collection":"Logs",` + laneWindowsJSON + `}`

func TestLaneMongoRunsAndAnswersM6(t *testing.T) {
	repo := &mongoLaneRepo{fakeRepository: &fakeRepository{}, run: func(_ context.Context, _, cmd string) (map[string]any, error) { return healthyMongo(cmd) }}
	mongoLaneEnv(t, repo, func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/mongo", laneMongoBodyJSON)
		if res.Code != http.StatusOK {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
		for _, secret := range []string{"mongo-secret", "mongo01.corp.example"} {
			if strings.Contains(res.Body.String(), secret) {
				t.Fatalf("%q leaked: %s", secret, res.Body)
			}
		}
		var list struct{ Days []laneDayDTO }
		decodeBody(t, e.get("/api/cases/"+id+"/lanes"), &list)
		if len(list.Days) != 1 || list.Days[0].Kind != "mongo" || list.Days[0].Lane.Answers["M6"] != "yes" {
			t.Fatalf("days = %+v", list.Days)
		}
	})
}

func TestLaneMongoDownMidReadFailsTheCheckNotTheServer(t *testing.T) {
	calls := 0
	repo := &mongoLaneRepo{fakeRepository: &fakeRepository{}, run: func(_ context.Context, _, cmd string) (map[string]any, error) {
		calls++
		if calls > 2 {
			return nil, errors.New("connection reset by peer mongo01.corp.example:27017")
		}
		return healthyMongo(cmd)
	}}
	mongoLaneEnv(t, repo, func(t *testing.T, e *caseEnv, id string) {
		calls = 0
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/mongo", laneMongoBodyJSON)
		if res.Code != http.StatusOK {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
		var out laneRunOut
		decodeBody(t, res, &out)
		if out.State != "Partly ready" || strings.Contains(res.Body.String(), "mongo01.corp.example") {
			t.Fatalf("state %q body %s", out.State, res.Body)
		}
		if strings.Contains(res.Body.String(), `"answers"`) {
			t.Fatalf("a read that broke off still answered: %s", res.Body)
		}
	})
}

func TestLaneMongoNoPermissionShowsScript(t *testing.T) {
	repo := &mongoLaneRepo{fakeRepository: &fakeRepository{}, run: func(context.Context, string, string) (map[string]any, error) {
		return nil, errors.New("not authorized on admin to execute command")
	}}
	mongoLaneEnv(t, repo, func(t *testing.T, e *caseEnv, id string) {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/mongo", laneMongoBodyJSON)
		var out laneRunOut
		decodeBody(t, res, &out)
		if res.Code != http.StatusOK || out.State != "Cannot test yet" || !strings.Contains(res.Body.String(), "clusterMonitor") {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
	})
}

func TestLaneMongoCannotConnectAndBadNames(t *testing.T) {
	repo := &mongoLaneRepo{fakeRepository: &fakeRepository{}, openErr: &audit.ConnectionError{Stage: "authentication", Code: "auth_failed", Message: "The username or password was not accepted."}}
	mongoLaneEnv(t, repo, func(t *testing.T, e *caseEnv, id string) {
		path := "/api/cases/" + id + "/lanes/mongo"
		repo.openErr = &audit.ConnectionError{Stage: "authentication", Code: "auth_failed", Message: "The username or password was not accepted."}
		res := e.json(http.MethodPost, path, laneMongoBodyJSON)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "auth_failed") {
			t.Fatalf("open error = %d %s", res.Code, res.Body)
		}
		for _, bad := range []string{`"database":"a b"`, `"database":""`, `"database":"x\"y"`} {
			body := `{"uri":"mongodb://h",` + bad + `,` + laneWindowsJSON + `}`
			if res := e.json(http.MethodPost, path, body); res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d", bad, res.Code)
			}
		}
		calls := repo.opened
		repo.openErr = nil
		repo.run = func(_ context.Context, _, cmd string) (map[string]any, error) { return healthyMongo(cmd) }
		if res = e.json(http.MethodPost, path, laneMongoBodyJSON); res.Code != http.StatusOK {
			t.Fatalf("after fix = %d %s", res.Code, res.Body)
		}
		overlap := strings.Replace(laneMongoBodyJSON, `"start":"2026-10-20T10:00:00Z"`, `"start":"2026-10-20T10:30:00Z"`, 1)
		if res = e.json(http.MethodPost, path, overlap); res.Code != http.StatusConflict {
			t.Fatalf("overlap = %d %s", res.Code, res.Body)
		}
		if repo.opened != calls+1 {
			t.Errorf("a refused day opened the server: %d vs %d", repo.opened, calls+1)
		}
	})
}

/* ------------------------------------------------------------ engine feed */

type healthyCase struct {
	*stubTraceCase
	runs []casestore.HealthRun
	err  error
}

func (h *healthyCase) AddHealthRun(_ context.Context, r casestore.HealthRun) (casestore.HealthRun, error) {
	r.ID = int64(len(h.runs) + 1)
	h.runs = append([]casestore.HealthRun{r}, h.runs...)
	return r, nil
}
func (h *healthyCase) HealthRuns(context.Context) ([]casestore.HealthRun, error) {
	return h.runs, h.err
}

func laneReportJSON(t *testing.T, kind, db string, inc dbhealth.Window, answers map[string]string, onset *time.Time) casestore.HealthRun {
	t.Helper()
	rep := dbhealth.Report{Kind: kind, Label: kind, Database: db, Overall: dbhealth.OK,
		Lane: &dbhealth.LaneResult{Incident: inc, Reference: dbhealth.Window{Start: inc.Start.Add(-48 * time.Hour), End: inc.Start.Add(-47 * time.Hour)}, Answers: answers, Onset: onset, Sources: []string{kind + " source"}}}
	enc, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	return casestore.HealthRun{Kind: kind, Label: kind, Status: "ok", Report: enc}
}

func TestApplyLaneRunsFeedsTheEngineAndLeavesAuditOnlyAlone(t *testing.T) {
	inc := dbhealth.Window{Start: lday(20, 10), End: lday(20, 11)}
	in := investigate.Input{Incident: investigate.Window{Start: inc.Start, End: inc.End}}

	// No day held: the input is unchanged.
	c := &healthyCase{stubTraceCase: &stubTraceCase{}}
	var read investigationRead
	applyLaneRuns(context.Background(), c, &in, &read)
	if in.Lanes != nil || in.LaneOnsets != nil || in.LaneSources != nil || len(read.Warnings) != 0 {
		t.Fatalf("audit-only input changed: %+v %+v", in, read)
	}
	// A case that cannot keep health runs at all behaves the same.
	applyLaneRuns(context.Background(), &stubTraceCase{}, &in, &read)
	if in.Lanes != nil {
		t.Fatal("changed")
	}

	early := lday(20, 10).Add(7 * time.Minute)
	later := lday(20, 10).Add(20 * time.Minute)
	c.runs = []casestore.HealthRun{
		laneReportJSON(t, "sql", "AuditDb", inc, map[string]string{"D5": "no", "D12": "no", "D3": "yes"}, &later),
		laneReportJSON(t, "sql", "OtherDb", inc, map[string]string{"D5": "yes", "D3": "no"}, &early),
		laneReportJSON(t, "mongo", "AuditDb", inc, map[string]string{"M6": "yes"}, nil),
		// A day that is not this incident is ignored.
		laneReportJSON(t, "sql", "AuditDb", dbhealth.Window{Start: lday(25, 10), End: lday(25, 11)}, map[string]string{"D13": "yes"}, nil),
		// An ordinary health run with no lane is ignored.
		{Kind: "sql", Label: "plain", Status: "ok", Report: []byte(`{"kind":"sql","label":"plain","findings":[]}`)},
	}
	in = investigate.Input{Incident: investigate.Window{Start: inc.Start, End: inc.End}}
	applyLaneRuns(context.Background(), c, &in, &read)
	want := map[string]investigate.Tri{"D5": investigate.Yes, "D12": investigate.No, "D3": investigate.Yes, "M6": investigate.Yes}
	if len(in.Lanes) != len(want) {
		t.Fatalf("lanes = %v", in.Lanes)
	}
	for id, v := range want {
		if in.Lanes[id] != v {
			t.Errorf("%s = %q, want %q", id, in.Lanes[id], v)
		}
	}
	if !in.LaneOnsets["sql"].Equal(early) || len(in.LaneOnsets) != 1 {
		t.Errorf("onsets = %v", in.LaneOnsets)
	}
	if len(in.LaneSources) != 2 {
		t.Errorf("sources = %v", in.LaneSources)
	}

	// A store that fails to list says so instead of silently dropping the days.
	c.err = errors.New("disk")
	in = investigate.Input{Incident: investigate.Window{Start: inc.Start, End: inc.End}}
	read = investigationRead{}
	applyLaneRuns(context.Background(), c, &in, &read)
	if in.Lanes != nil || len(read.Warnings) != 1 {
		t.Errorf("failed read: %+v %+v", in.Lanes, read.Warnings)
	}
}

func TestGrantScriptsRoute(t *testing.T) {
	healthEnv(t, &fakeSavedSQL{}, func(t *testing.T, e *caseEnv, _ string) {
		get := func(q string) (int, string) {
			res := e.get("/api/grant-scripts?" + q)
			return res.Code, res.Body.String()
		}
		code, body := get("kind=sql&version=2022&login=unravis_ro&database=AuditDb")
		for _, want := range []string{"VIEW SERVER PERFORMANCE STATE", "VIEW ANY ERROR LOG", "[unravis_ro]", "[AuditDb]", "SQLAgentReaderRole"} {
			if code != http.StatusOK || !strings.Contains(body, want) {
				t.Errorf("2022 lacks %q (%d): %s", want, code, body)
			}
		}
		code, body = get("kind=sql&version=2019&login=unravis_ro&database=AuditDb")
		if code != http.StatusOK || !strings.Contains(body, "VIEW SERVER STATE") || strings.Contains(body, "PERFORMANCE STATE") || strings.Contains(body, "VIEW ANY ERROR LOG") {
			t.Errorf("2019 = %d %s", code, body)
		}
		code, body = get("kind=mongo&login=u&database=AuditDb")
		if code != http.StatusOK || !strings.Contains(body, "clusterMonitor") {
			t.Errorf("mongo = %d %s", code, body)
		}
		code, body = get("kind=mongo-profiler&database=AuditDb")
		if code != http.StatusOK || !strings.Contains(body, "setProfilingLevel") {
			t.Errorf("profiler = %d %s", code, body)
		}
		// A name that tries to break out of the script is turned into a placeholder, never into a statement.
		code, body = get("kind=sql&version=2022&login=" + "x%5D%3B%20DROP%20LOGIN%20sa%3B--" + "&database=d")
		if code != http.StatusOK || strings.Contains(body, "DROP LOGIN") {
			t.Errorf("injection = %d %s", code, body)
		}
		for _, bad := range []string{"kind=sql&version=2017", "kind=oracle", "", "kind=sql&version=2022&login=" + strings.Repeat("a", 300)} {
			if code, _ := get(bad); code != http.StatusBadRequest {
				t.Errorf("%q = %d", bad, code)
			}
		}
		if res := e.json(http.MethodPost, "/api/grant-scripts?kind=mongo", ""); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST = %d", res.Code)
		}
	})
}
