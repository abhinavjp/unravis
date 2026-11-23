package server

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sqlaccess"
	"api-audit-explorer/internal/sqlconn"
)

const sqlSetupPath = "/api/blackbox/this-pc/sql/setup"

func readyReport() sqlaccess.Report {
	return sqlaccess.Report{Reached: true, Ready: true, Verdict: sqlaccess.VerdictReady, Checks: []sqlaccess.Check{{ID: "connect", State: "passed", Label: "x", Detail: "y"}}}
}

func TestSQLSetupWindowsSigninNeedsNoConnectionAndPrintsTheGrant(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post(sqlSetupPath, `{"mode":"windows"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"grantScript"`) || !strings.Contains(res.Body.String(), `"loginSaved":false`) || !strings.Contains(res.Body.String(), `"started":false`) {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	if b.pc.loginFileOK {
		t.Error("Windows sign-in handed over a login file")
	}
}

func TestSQLSetupChecksAccessFirstAndChangesNothingWhenNotReady(t *testing.T) {
	stubAccess(t, sqlaccess.Report{Reached: true, Verdict: sqlaccess.VerdictNeedsGrant, Script: "GRANT VIEW SERVER STATE TO [u];\n", Checks: []sqlaccess.Check{{ID: "read", State: "failed", Label: "x", Detail: "y"}}})
	b := newBBEnv(t, nil)
	before := len(b.pc.callLog())
	res := b.post(sqlSetupPath, `{"mode":"login","enable":true,"server":"db01","auth":"sql","username":"u","password":"pw-SECRET","encrypt":"mandatory"}`)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"reason":"access"`) || !strings.Contains(res.Body.String(), "GRANT VIEW SERVER STATE") {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "pw-SECRET") {
		t.Fatal("the typed password is in the answer")
	}
	if got := b.pc.callLog(); len(got) != before {
		t.Fatalf("the helper was called although the login is not ready: %v", got)
	}
}

func TestSQLSetupWithATypedLoginHandsTheHelperAFileThenRemovesIt(t *testing.T) {
	seen := stubAccess(t, readyReport())
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMP", tmp)
	b := newBBEnv(t, nil)
	res := b.post(sqlSetupPath, `{"mode":"login","enable":true,"server":"db01","auth":"sql","username":"u","password":"pw-SECRET","encrypt":"mandatory"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"loginSaved":true`) || !strings.Contains(res.Body.String(), `"started":true`) {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "pw-SECRET") {
		t.Fatal("the typed password is in the answer")
	}
	if !b.pc.loginFileOK {
		t.Fatal("the helper was not handed a login file with the right hash")
	}
	if len(*seen) == 0 || (*seen)[0].Password != "pw-SECRET" {
		t.Fatalf("the access check did not use the typed login: %+v", *seen)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("a login file folder was left behind: %d entries", len(ents))
	}
}

func TestSQLSetupRefusals(t *testing.T) {
	stubAccess(t, readyReport())
	b := newBBEnv(t, nil)
	for name, c := range map[string]struct {
		body string
		code int
		want string
	}{
		"no mode":         {`{}`, http.StatusBadRequest, "Pick how"},
		"windows sign-in": {`{"mode":"login","server":"db01","auth":"windows"}`, http.StatusUnprocessableEntity, "Only a SQL login"},
		"optional":        {`{"mode":"login","server":"db01","auth":"sql","username":"u","password":"p","encrypt":"optional"}`, http.StatusUnprocessableEntity, "always encrypts"},
		"bad server":      {`{"mode":"login","server":"db01;x=1","auth":"sql","username":"u","password":"p","encrypt":"mandatory"}`, http.StatusUnprocessableEntity, "cannot be used"},
	} {
		res := b.post(sqlSetupPath, c.body)
		if res.Code != c.code || !strings.Contains(res.Body.String(), c.want) {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	if res := b.e.get(sqlSetupPath); res.Code == http.StatusOK {
		t.Error("GET must not work")
	}
}

func TestSQLClearLogin(t *testing.T) {
	b := newBBEnv(t, nil)
	if res := b.post("/api/blackbox/this-pc/sql/login/clear", ""); res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body)
	}
	if got := b.pc.callLog(); got[len(got)-1] != "clear-sql-login" {
		t.Fatalf("calls %v", got)
	}
}

// heldSQLList is a saved list whose password the keeper holds: the app process never sees it.
type heldSQLList struct {
	fakeSavedSQL
	loginFile blackboxLoginFile
	asked     int
}

type blackboxLoginFile = blackbox.SQLLoginFile

func (h *heldSQLList) Held(string) (bool, error) { return true, nil }
func (h *heldSQLList) TestSaved(context.Context, keeper.SQLRef) (dbhealth.TestResult, string, error) {
	return dbhealth.TestResult{}, "", nil
}
func (h *heldSQLList) HealthSaved(context.Context, keeper.SQLRef, string) (dbhealth.Report, error) {
	return dbhealth.Report{}, nil
}
func (h *heldSQLList) LaneSaved(context.Context, keeper.SQLRef, string, dbhealth.LaneRequest) (dbhealth.Report, error) {
	return dbhealth.Report{}, nil
}
func (h *heldSQLList) AccessSaved(context.Context, keeper.SQLRef) (sqlaccess.Report, error) {
	return readyReport(), nil
}
func (h *heldSQLList) LoginFileSaved(context.Context, keeper.SQLRef) (blackbox.SQLLoginFile, error) {
	h.asked++
	return h.loginFile, nil
}

func storedFor(t *testing.T) bbsql.StoredLogin {
	t.Helper()
	s, err := sqlaccess.StoredLoginFor(dbhealth.SQLTarget{Server: "db01", Auth: "sql", Username: "u", Password: "from-keeper", Encrypt: "mandatory"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLSetupWithASavedLoginTheKeeperMakesTheFile(t *testing.T) {
	// The keeper (not this process) writes the file; here a file stands in for what it wrote.
	f, err := blackbox.WriteSQLLoginFile(t.TempDir(), storedFor(t))
	if err != nil {
		t.Fatal(err)
	}
	saved := &heldSQLList{fakeSavedSQL: fakeSavedSQL{in: sqlconn.Input{Server: "db01", Auth: "sql", Username: "u", Encrypt: "mandatory"}}, loginFile: f}
	t.Cleanup(func() { blackbox.RemoveSQLLoginFile(f) })
	b := newBBEnvWith(t, nil, WithSQLConnections(saved))
	res := b.post(sqlSetupPath, `{"mode":"login","enable":true,"savedId":"saved1"}`)
	if res.Code != http.StatusOK || saved.asked != 1 || !b.pc.loginFileOK {
		t.Fatalf("setup = %d %s asked=%d fileOK=%v", res.Code, res.Body, saved.asked, b.pc.loginFileOK)
	}
}
