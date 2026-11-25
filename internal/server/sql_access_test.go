package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/sqlaccess"
	"api-audit-explorer/internal/sqlconn"
)

func stubAccess(t *testing.T, rep sqlaccess.Report) *[]dbhealth.SQLTarget {
	t.Helper()
	var seen []dbhealth.SQLTarget
	old := accessSQLFn
	t.Cleanup(func() { accessSQLFn = old })
	accessSQLFn = func(_ context.Context, tg dbhealth.SQLTarget) sqlaccess.Report {
		seen = append(seen, tg)
		return rep
	}
	return &seen
}

func TestSQLAccessCheckReportsAndPrintsTheScriptOnlyFromTheReport(t *testing.T) {
	seen := stubAccess(t, sqlaccess.Report{Reached: true, Verdict: sqlaccess.VerdictNeedsGrant, Script: "GRANT VIEW SERVER STATE TO [u];\n",
		Checks: []sqlaccess.Check{{ID: "read", State: "failed", Label: "x", Detail: "y"}}})
	healthEnv(t, &fakeSavedSQL{}, func(t *testing.T, e *caseEnv, _ string) {
		res := e.json(http.MethodPost, "/api/sql-connections/access", `{"server":"db01","auth":"sql","username":"u","password":"pw","encrypt":"mandatory"}`)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"verdict":"needs-grant"`) || !strings.Contains(res.Body.String(), "GRANT VIEW SERVER STATE") {
			t.Fatalf("access = %d %s", res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), `"pw"`) || strings.Contains(res.Body.String(), "password") {
			t.Fatalf("typed password echoed: %s", res.Body)
		}
		if res = e.json(http.MethodPost, "/api/sql-connections/access", `{"server":" "}`); res.Code != http.StatusBadRequest {
			t.Fatalf("empty server = %d", res.Code)
		}
		if res = e.get("/api/sql-connections/access"); res.Code == http.StatusOK {
			t.Fatalf("GET must not work: %d", res.Code)
		}
	})
	if got := *seen; len(got) < 1 || got[0].Server != "db01" || got[0].Password != "pw" {
		t.Fatalf("target = %+v", got)
	}
}

func TestSQLAccessCheckUsesTheSavedLoginServerSide(t *testing.T) {
	seen := stubAccess(t, sqlaccess.Report{Reached: true, Ready: true, Verdict: sqlaccess.VerdictReady})
	saved := &fakeSavedSQL{in: sqlconn.Input{Server: "db01", Auth: "sql", Username: "u", Password: "stored-pw", Encrypt: "mandatory"}}
	healthEnv(t, saved, func(t *testing.T, e *caseEnv, _ string) {
		res := e.json(http.MethodPost, "/api/sql-connections/access", `{"savedId":"saved1"}`)
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "stored-pw") || !strings.Contains(res.Body.String(), `"checks":[]`) {
			t.Fatalf("saved access = %d %s", res.Code, res.Body)
		}
		if res = e.json(http.MethodPost, "/api/sql-connections/access", `{"savedId":"nope"}`); res.Code != http.StatusNotFound {
			t.Fatalf("unknown saved id = %d", res.Code)
		}
	})
	if got := *seen; len(got) < 1 || got[0].Password != "stored-pw" {
		t.Fatalf("targets = %+v", got)
	}
}

func TestDemoAccessReportShowsTheScriptPathAndNeverConnects(t *testing.T) {
	r := demoAccessReport()
	if r.Ready || r.Verdict != sqlaccess.VerdictNeedsGrant || !strings.Contains(r.Script, "GRANT VIEW SERVER PERFORMANCE STATE TO [unravis_ro];") {
		t.Fatalf("%+v", r)
	}
}
