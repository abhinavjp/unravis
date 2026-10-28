package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sqlconn"
)

// demoLaneEnv is a --demo server with no saved-connection stores given: it must bring its own fakes. Any real open or test fails the test.
func demoLaneEnv(t *testing.T) (*caseEnv, string) {
	t.Helper()
	oldTest, oldOpen := testSQLFn, openSQLFn
	t.Cleanup(func() { testSQLFn, openSQLFn = oldTest, oldOpen })
	testSQLFn = func(context.Context, dbhealth.SQLTarget) (dbhealth.TestResult, error) {
		t.Error("the demo opened a real SQL Server test")
		return dbhealth.TestResult{}, nil
	}
	openSQLFn = func(context.Context, dbhealth.SQLTarget) (healthQueryer, error) {
		t.Error("the demo opened a real SQL Server connection")
		return nil, nil
	}
	st := casestore.NewMemory()
	e := &caseEnv{t: t, store: st, h: New(&mongoLaneRepo{fakeRepository: &fakeRepository{}, openErr: context.Canceled}, testAssets(), testToken, true,
		WithCases(st, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})))}
	return e, e.newCase("demo lanes")
}

func demoSQLBody(database string) string {
	return `{"server":"anything.example","auth":"sql","username":"someone","password":"typed-secret","database":"` + database + `",` + laneWindowsJSON + `}`
}

func demoMongoBody(database string) string {
	return `{"uri":"mongodb://anything.example","username":"someone","password":"typed-secret","database":"` + database + `","collection":"Logs",` + laneWindowsJSON + `}`
}

type demoDay struct {
	State string
	Lane  *dbhealth.LaneResult
}

func demoDayOf(t *testing.T, e *caseEnv, id, kind string) demoDay {
	t.Helper()
	var list struct{ Days []laneDayDTO }
	decodeBody(t, e.get("/api/cases/"+id+"/lanes"), &list)
	for _, d := range list.Days {
		if d.Kind == kind {
			return demoDay{State: d.State, Lane: d.Lane}
		}
	}
	t.Fatalf("no %s day held: %+v", kind, list.Days)
	return demoDay{}
}

// leakWords are the words a response must not carry. The login name is the owner's own and a grant script has to name it, so it is only
// checked where no script is expected.
func leakWords(hasScript bool) []string {
	if hasScript {
		return []string{"typed-secret", "anything.example"}
	}
	return []string{"typed-secret", "anything.example", "someone"}
}

func fixesOf(l *dbhealth.LaneResult) int {
	n := 0
	for _, c := range l.Checks {
		if c.Fix != nil {
			n++
		}
	}
	return n
}

func TestDemoServesTheSavedConnectionLists(t *testing.T) {
	e, _ := demoLaneEnv(t)
	var sqlList []sqlconn.Recent
	res := e.get("/api/sql-connections")
	if res.Code != http.StatusOK {
		t.Fatalf("sql list = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &sqlList)
	if len(sqlList) != 1 || sqlList[0].Label != "Demo SQL Server" {
		t.Fatalf("sql list = %+v", sqlList)
	}
	if res = e.get("/api/connections/recent"); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Demo MongoDB") {
		t.Fatalf("mongo list = %d %s", res.Code, res.Body)
	}
	// Saving keeps the details but never the password, and the answer does not carry one.
	res = e.json(http.MethodPost, "/api/sql-connections", `{"label":"Mine","server":"db.example:1444","auth":"sql","username":"u","password":"typed-secret","rememberPassword":true,"database":"AuditDb"}`)
	if res.Code != http.StatusOK && res.Code != http.StatusCreated {
		t.Fatalf("save = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "typed-secret") {
		t.Fatalf("the save answer carries the password: %s", res.Body)
	}
	if got := e.get("/api/sql-connections").Body.String(); strings.Contains(got, "typed-secret") || !strings.Contains(got, `"Mine"`) {
		t.Fatalf("list after save = %s", got)
	}
	var saved []sqlconn.Recent
	decodeBody(t, e.get("/api/sql-connections"), &saved)
	var id string
	for _, c := range saved {
		if c.Label == "Mine" {
			id = c.ID
		}
	}
	if id == "" {
		t.Fatalf("saved connection missing: %+v", saved)
	}
	if res = e.json(http.MethodDelete, "/api/sql-connections/"+id, ""); res.Code != http.StatusOK && res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	if strings.Contains(e.get("/api/sql-connections").Body.String(), `"Mine"`) {
		t.Fatal("deleted connection is still listed")
	}
}

func TestDemoSavedSQLStoreForgetsPasswordsAndHonoursTheLimit(t *testing.T) {
	s := newDemoSQLStore(lday(20, 9))
	rec, err := s.Save(sqlconn.Input{Server: "a.example", Auth: sqlconn.AuthSQL, Username: "u", Password: "pw", RememberPassword: true, Database: "D1"})
	if err != nil || !rec.HasPassword {
		t.Fatalf("save = %+v, %v", rec, err)
	}
	in, err := s.Resolve(rec.ID)
	if err != nil || in.Password != "" {
		t.Fatalf("resolve = %+v, %v: the demo kept a password", in, err)
	}
	if _, err := s.Save(sqlconn.Input{Server: " ", Auth: sqlconn.AuthSQL}); err == nil {
		t.Fatal("an empty server was saved")
	}
	for i := 0; i < sqlconn.MaxRecords+2; i++ {
		_, err = s.Save(sqlconn.Input{Server: "h" + string(rune('a'+i)) + ".example", Auth: sqlconn.AuthWindows, Database: "D"})
	}
	if err != sqlconn.ErrFull {
		t.Fatalf("past the limit: err = %v, want ErrFull", err)
	}
	if err := s.Delete("missing"); err != sqlconn.ErrNotFound {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestDemoConnectionTestAnswersWithoutConnecting(t *testing.T) {
	e, _ := demoLaneEnv(t)
	res := e.json(http.MethodPost, "/api/sql-connections/test", `{"server":"anything.example","auth":"sql","username":"u","password":"typed-secret"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"ok":true`) || !strings.Contains(res.Body.String(), "NoGrantDb") {
		t.Fatalf("test = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "typed-secret") {
		t.Fatalf("the test answer carries the password: %s", res.Body)
	}
}

func TestDemoSQLLaneScenarios(t *testing.T) {
	for _, tc := range []struct {
		database, state string
		fixes           bool
	}{
		{"AuditDb", "Ready", false},
		{"NoGrantDb", "", true},
		{"EmptyDb", "Cannot test yet", false},
	} {
		t.Run(tc.database, func(t *testing.T) {
			e, id := demoLaneEnv(t)
			res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", demoSQLBody(tc.database))
			if res.Code != http.StatusOK {
				t.Fatalf("run = %d %s", res.Code, res.Body)
			}
			for _, secret := range leakWords(tc.fixes) {
				if strings.Contains(res.Body.String(), secret) {
					t.Fatalf("%q leaked: %s", secret, res.Body)
				}
			}
			day := demoDayOf(t, e, id, "sql")
			if tc.state != "" && day.State != tc.state {
				t.Fatalf("state = %q, want %q", day.State, tc.state)
			}
			if got := fixesOf(day.Lane) > 0; got != tc.fixes {
				t.Fatalf("fix script offered = %v, want %v (%+v)", got, tc.fixes, day.Lane.Checks)
			}
		})
	}
}

func TestDemoSQLLaneStillRefusesAClosedCaseAndAnOverlap(t *testing.T) {
	e, id := demoLaneEnv(t)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", demoSQLBody("AuditDb")); res.Code != http.StatusOK {
		t.Fatalf("first = %d %s", res.Code, res.Body)
	}
	overlap := strings.Replace(demoSQLBody("AuditDb"), `"start":"2026-10-20T10:00:00Z"`, `"start":"2026-10-20T10:30:00Z"`, 1)
	overlap = strings.Replace(overlap, `"end":"2026-10-20T11:00:00Z"`, `"end":"2026-10-20T11:30:00Z"`, 1)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", overlap); res.Code != http.StatusConflict {
		t.Fatalf("overlap = %d %s", res.Code, res.Body)
	}
	if res := e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open"); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	for _, p := range []string{"sql", "mongo"} {
		body := demoSQLBody("AuditDb")
		if p == "mongo" {
			body = demoMongoBody("AuditDb")
		}
		res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/"+p, body)
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"reason":"case-closed"`) {
			t.Errorf("%s on a closed demo case = %d %s", p, res.Code, res.Body)
		}
	}
}

func TestDemoMongoLaneScenarios(t *testing.T) {
	for _, tc := range []struct {
		database string
		fixes    bool
		guide    bool
	}{
		{"AuditDb", false, false},
		{"NoGrantDb", true, false},
		{"ProfilerOffDb", true, true},
	} {
		t.Run(tc.database, func(t *testing.T) {
			e, id := demoLaneEnv(t)
			res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/mongo", demoMongoBody(tc.database))
			if res.Code != http.StatusOK {
				t.Fatalf("run = %d %s", res.Code, res.Body)
			}
			for _, secret := range leakWords(tc.fixes) {
				if strings.Contains(res.Body.String(), secret) {
					t.Fatalf("%q leaked: %s", secret, res.Body)
				}
			}
			day := demoDayOf(t, e, id, "mongo")
			if got := fixesOf(day.Lane) > 0; got != tc.fixes {
				t.Fatalf("fix script offered = %v, want %v (%+v)", got, tc.fixes, day.Lane.Checks)
			}
			if got := day.Lane.Guide != nil; got != tc.guide {
				t.Fatalf("profiler guide shown = %v, want %v", got, tc.guide)
			}
			if tc.database == "AuditDb" && day.State != "Ready" {
				t.Fatalf("state = %q, want Ready", day.State)
			}
		})
	}
}

func TestDemoSavedMongoStoreKeepsNoPassword(t *testing.T) {
	s := newDemoHistory(lday(20, 9))
	rec, err := s.Remember("Mine", audit.ConnectionInput{URI: "mongodb://x.example", Username: "u", Password: "pw"}, "Logs", true)
	if err != nil || !rec.HasCredentials {
		t.Fatalf("remember = %+v, %v", rec, err)
	}
	got, err := s.Resolve(audit.ConnectionInput{SavedConnectionID: rec.ID})
	if err != nil || got.Password != "" || got.URI != "mongodb://x.example" {
		t.Fatalf("resolve = %+v, %v", got, err)
	}
	if err := s.Delete(rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(audit.ConnectionInput{SavedConnectionID: rec.ID}); err == nil {
		t.Fatal("a deleted connection still resolves")
	}
}
