package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/sqlconn"
)

type fakeSavedSQL struct {
	in      sqlconn.Input
	saveErr error
}

func (f *fakeSavedSQL) List() ([]sqlconn.Recent, error)               { return nil, nil }
func (f *fakeSavedSQL) Save(sqlconn.Input) (sqlconn.Recent, error)    { return sqlconn.Recent{}, f.saveErr }
func (f *fakeSavedSQL) Delete(string) error                           { return nil }
func (f *fakeSavedSQL) Resolve(id string) (sqlconn.Input, error) {
	if id != "saved1" {
		return sqlconn.Input{}, sqlconn.ErrNotFound
	}
	return f.in, nil
}

type emptyQueryer struct{}

func (emptyQueryer) Query(context.Context, string) ([]dbhealth.Row, error) { return nil, nil }
func (emptyQueryer) Close()                                                  {}

// stubDrivers replaces the SQL driver calls and records the login each one received.
func stubDrivers(t *testing.T) *[]dbhealth.SQLTarget {
	t.Helper()
	var seen []dbhealth.SQLTarget
	oldTest, oldOpen := testSQLFn, openSQLFn
	t.Cleanup(func() { testSQLFn, openSQLFn = oldTest, oldOpen })
	testSQLFn = func(_ context.Context, tg dbhealth.SQLTarget) (dbhealth.TestResult, error) {
		seen = append(seen, tg)
		switch tg.Server {
		case "bad":
			return dbhealth.TestResult{}, errors.New("Login failed for user 'svc_secret' on PRODSQL01")
		case "nolist":
			return dbhealth.TestResult{Version: "16.0", Databases: []string{}}, &dbhealth.ConnectError{Code: "list", Message: "Connected, but this login could not list the databases. Type the database name instead."}
		}
		return dbhealth.TestResult{Version: "16.0.1", Edition: "Developer", Databases: []string{"Audit", "Other"}}, nil
	}
	openSQLFn = func(_ context.Context, tg dbhealth.SQLTarget) (healthQueryer, error) {
		seen = append(seen, tg)
		if tg.Server == "bad" {
			return nil, errors.New("dial tcp: lookup PRODSQL01: no such host")
		}
		return emptyQueryer{}, nil
	}
	return &seen
}

func healthEnv(t *testing.T, saved *fakeSavedSQL, fn func(t *testing.T, e *caseEnv, id string)) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{}, WithSQLConnections(saved))
			fn(t, e, e.newCase("health"))
		})
	}
}

func TestSQLTestListsDatabasesAndHidesDriverText(t *testing.T) {
	seen := stubDrivers(t)
	healthEnv(t, &fakeSavedSQL{}, func(t *testing.T, e *caseEnv, _ string) {
		res := e.json(http.MethodPost, "/api/sql-connections/test", `{"server":"db01","auth":"sql","username":"u","password":"pw","encrypt":"mandatory"}`)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"Audit"`) || !strings.Contains(res.Body.String(), `"ok":true`) {
			t.Fatalf("test = %d %s", res.Code, res.Body)
		}
		res = e.json(http.MethodPost, "/api/sql-connections/test", `{"server":"nolist","auth":"windows"}`)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "listError") || !strings.Contains(res.Body.String(), `"databases":[]`) {
			t.Fatalf("nolist = %d %s", res.Code, res.Body)
		}
		res = e.json(http.MethodPost, "/api/sql-connections/test", `{"server":"bad","auth":"windows"}`)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "login_failed") {
			t.Fatalf("bad = %d %s", res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), "svc_secret") || strings.Contains(res.Body.String(), "PRODSQL01") {
			t.Fatalf("driver text leaked: %s", res.Body)
		}
		if res = e.json(http.MethodPost, "/api/sql-connections/test", `{"server":" "}`); res.Code != http.StatusBadRequest {
			t.Fatalf("empty server = %d", res.Code)
		}
	})
	if len(*seen) < 3 {
		t.Fatalf("driver calls = %d", len(*seen))
	}
}

func TestSQLTestUsesSavedPasswordServerSide(t *testing.T) {
	seen := stubDrivers(t)
	saved := &fakeSavedSQL{in: sqlconn.Input{Server: "db01", Auth: "sql", Username: "u", Password: "stored-pw", Database: "Audit", Encrypt: "mandatory"}}
	healthEnv(t, saved, func(t *testing.T, e *caseEnv, _ string) {
		res := e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"saved1"}`)
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "stored-pw") {
			t.Fatalf("saved test = %d %s", res.Code, res.Body)
		}
		res = e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"saved1","password":"typed","database":"Other"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("override = %d %s", res.Code, res.Body)
		}
		if res = e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"nope"}`); res.Code != http.StatusNotFound {
			t.Fatalf("unknown saved id = %d", res.Code)
		}
	})
	if got := *seen; len(got) < 2 || got[0].Password != "stored-pw" || got[1].Password != "typed" || got[1].Database != "Other" {
		t.Fatalf("targets = %+v", got)
	}
}

func TestHealthRunIsStoredAndListed(t *testing.T) {
	stubDrivers(t)
	healthEnv(t, &fakeSavedSQL{}, func(t *testing.T, e *caseEnv, id string) {
		path := "/api/cases/" + id + "/health"
		var list struct{ Runs []healthRunDTO }
		res := e.get(path)
		decodeBody(t, res, &list)
		if res.Code != http.StatusOK || list.Runs == nil || len(list.Runs) != 0 {
			t.Fatalf("empty list = %d %s", res.Code, res.Body)
		}
		res = e.json(http.MethodPost, path+"/run", `{"kind":"sql","sql":{"server":"db01.corp.example","auth":"sql","username":"u","password":"s3cret-pw","database":"Audit","encrypt":"mandatory"}}`)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"saved":true`) {
			t.Fatalf("run = %d %s", res.Code, res.Body)
		}
		for _, secret := range []string{"s3cret-pw", "db01.corp.example"} {
			if strings.Contains(res.Body.String(), secret) {
				t.Fatalf("%q leaked into the report: %s", secret, res.Body)
			}
		}
		decodeBody(t, e.get(path), &list)
		if len(list.Runs) != 1 || list.Runs[0].Kind != "sql" || list.Runs[0].ID == 0 {
			t.Fatalf("list = %+v", list.Runs)
		}
	})
}

func TestHealthRunRefusals(t *testing.T) {
	stubDrivers(t)
	healthEnv(t, &fakeSavedSQL{}, func(t *testing.T, e *caseEnv, id string) {
		run := "/api/cases/" + id + "/health/run"
		for body, want := range map[string]int{
			`{"kind":"oracle"}`:                                  http.StatusBadRequest,
			`{"kind":"sql"}`:                                     http.StatusBadRequest,
			`{"kind":"sql","sql":{"server":"bad","auth":"windows"}}`: http.StatusBadRequest,
			`{"kind":"mongo","mongo":{"uri":"mongodb://h"}}`:       http.StatusBadRequest,
			`{"kind":"mongo","mongo":{"uri":"mongodb://h","database":"a b"}}`: http.StatusBadRequest,
			`{"kind":"mongo","mongo":{"uri":"mongodb://h","database":"audit"}}`: http.StatusBadRequest, // repository cannot open a command client
		} {
			if res := e.json(http.MethodPost, run, body); res.Code != want {
				t.Errorf("%s = %d %s", body, res.Code, res.Body)
			}
		}
		if res := e.json(http.MethodPost, "/api/cases/not-a-case/health/run", `{"kind":"sql"}`); res.Code != http.StatusNotFound {
			t.Errorf("unknown case = %d", res.Code)
		}
		healthBusy.Lock()
		res := e.json(http.MethodPost, run, `{"kind":"sql","sql":{"server":"db01","auth":"windows"}}`)
		healthBusy.Unlock()
		if res.Code != http.StatusConflict {
			t.Errorf("second run while busy = %d %s", res.Code, res.Body)
		}
	})
}

func TestHealthStoreRoundTrip(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{}, func(h *handler) { h.tests.limit = 1000 })
			id := e.newCase("store")
			var list struct{ Runs []healthRunDTO }
			for i := 0; i < casestore.MaxHealthRuns+3; i++ {
				stubDrivers(t)
				if res := e.json(http.MethodPost, "/api/cases/"+id+"/health/run", `{"kind":"sql","sql":{"server":"db01","auth":"windows"}}`); res.Code != http.StatusOK {
					t.Fatalf("run %d = %d %s", i, res.Code, res.Body)
				}
			}
			decodeBody(t, e.get("/api/cases/"+id+"/health"), &list)
			if len(list.Runs) != casestore.MaxHealthRuns || list.Runs[0].ID < list.Runs[len(list.Runs)-1].ID {
				t.Fatalf("kept %d runs, newest first expected", len(list.Runs))
			}
		})
	}
}
