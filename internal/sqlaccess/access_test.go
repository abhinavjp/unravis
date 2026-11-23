package sqlaccess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/bbsql"
)

// fake answers by the tag comment each fixed statement carries.
type fake struct {
	who      map[string]any
	role     map[string]any
	perms    []map[string]any
	writable []map[string]any
	deny     map[string]bool // statement tag -> permission error
	whoErr   error
	sent     []string
}

func tag(text string) string {
	i := strings.LastIndex(text, "/* unravis:")
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(text[i+len("/* unravis:"):]), "*/")
}

func (f *fake) Query(_ context.Context, text string) ([]map[string]any, error) {
	t := strings.TrimSpace(tag(text))
	f.sent = append(f.sent, t)
	if f.deny[t] {
		return nil, errors.New("The SELECT permission was denied on the object")
	}
	switch t {
	case "access:who":
		if f.whoErr != nil {
			return nil, f.whoErr
		}
		return []map[string]any{f.who}, nil
	case "bb:ceiling_role":
		return []map[string]any{f.role}, nil
	case "bb:ceiling_perms":
		return f.perms, nil
	case "access:writable":
		return f.writable, nil
	}
	return []map[string]any{}, nil
}

func goodRole() map[string]any {
	return map[string]any{"sysadmin": int64(0), "securityadmin": int64(0), "serveradmin": int64(0), "setupadmin": int64(0), "processadmin": int64(0),
		"dbcreator": int64(0), "bulkadmin": int64(0), "diskadmin": int64(0), "principal_missing": int64(0), "perm_empty": int64(0), "perm_outside": int64(0), "connect_missing": int64(0)}
}

func goodFake() *fake {
	return &fake{
		who:  map[string]any{"login_name": "unravis_reader", "major": int64(15), "version": "15.0.4123.1", "view_state": int64(1), "view_any_db": int64(1)},
		role: goodRole(), deny: map[string]bool{},
	}
}

func state(r Report, id string) string {
	for _, c := range r.Checks {
		if c.ID == id {
			return c.State
		}
	}
	return "none"
}

func TestReadyLoginPassesEverything(t *testing.T) {
	r := Run(context.Background(), goodFake(), nil)
	if !r.Ready || r.Verdict != VerdictReady || r.Script != "" || r.Version != "15.0.4123.1" {
		t.Fatalf("%+v", r)
	}
	for _, id := range []string{CheckConnect, CheckRights, CheckRead, CheckWrite} {
		if state(r, id) != Passed {
			t.Errorf("%s = %s", id, state(r, id))
		}
	}
}

func TestAccessSendsOnlyFixedStatements(t *testing.T) {
	f := goodFake()
	Run(context.Background(), f, nil)
	allowed := map[string]bool{"access:who": true, "access:writable": true}
	for _, s := range f.sent {
		if !allowed[s] && !bbsql.IsAllowed(mustStatement(t, s)) {
			t.Errorf("sent %q, not a fixed statement", s)
		}
	}
	if len(f.sent) < 10 {
		t.Fatalf("expected the control, sampler and writable statements, got %v", f.sent)
	}
}

func mustStatement(t *testing.T, tag string) string {
	s, ok := bbsql.Statement(tag)
	if !ok {
		t.Fatalf("unknown tag %q", tag)
	}
	return s
}

func TestMissingViewStateShowsOnlyTheNeededGrant(t *testing.T) {
	f := goodFake()
	f.who["view_state"] = int64(0)
	f.deny["bb:waits"] = true
	r := Run(context.Background(), f, nil)
	if r.Ready || r.Verdict != VerdictNeedsGrant || state(r, CheckRead) != Failed {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Script, "GRANT VIEW SERVER STATE TO [unravis_reader];") || strings.Contains(r.Script, "VIEW ANY DATABASE") {
		t.Fatalf("script:\n%s", r.Script)
	}
	if !strings.Contains(r.Checks[2].Detail, "wait statistics") {
		t.Fatalf("detail %q", r.Checks[2].Detail)
	}
}

func TestSQL2022UsesPerformanceStateRight(t *testing.T) {
	f := goodFake()
	f.who["major"] = int64(16)
	f.who["view_state"] = int64(0)
	f.who["view_any_db"] = int64(0)
	r := Run(context.Background(), f, nil)
	if !strings.Contains(r.Script, "GRANT VIEW SERVER PERFORMANCE STATE TO [unravis_reader];") || !strings.Contains(r.Script, "GRANT VIEW ANY DATABASE TO [unravis_reader];") {
		t.Fatalf("script:\n%s", r.Script)
	}
}

func TestMissingViewAnyDatabaseAlone(t *testing.T) {
	f := goodFake()
	f.who["view_any_db"] = int64(0)
	r := Run(context.Background(), f, nil)
	if r.Verdict != VerdictNeedsGrant || r.Ready || strings.Contains(r.Script, "VIEW SERVER") {
		t.Fatalf("%+v\n%s", r, r.Script)
	}
}

func TestTooManyRightsRefusedWithReadOnlyLoginScript(t *testing.T) {
	f := goodFake()
	f.role["sysadmin"] = int64(1)
	r := Run(context.Background(), f, nil)
	if r.Ready || r.Verdict != VerdictTooMany || state(r, CheckRights) != Failed {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Script, "CREATE LOGIN [unravis_reader]") || strings.Contains(r.Script, "GRANT CONTROL") {
		t.Fatalf("script:\n%s", r.Script)
	}
}

func TestUnreadableRightsFailClosed(t *testing.T) {
	f := goodFake()
	f.role["perm_empty"] = int64(1)
	r := Run(context.Background(), f, nil)
	if r.Ready || r.Verdict != VerdictUnreadable {
		t.Fatalf("%+v", r)
	}
}

func TestWritableDatabasesWarnButDoNotBlock(t *testing.T) {
	f := goodFake()
	f.writable = []map[string]any{{"name": "Payroll"}, {"name": "Orders"}}
	r := Run(context.Background(), f, nil)
	if !r.Ready || state(r, CheckWrite) != Warning || r.WritableDatabases != 2 || r.WritableNames[0] != "Payroll" {
		t.Fatalf("%+v", r)
	}
}

func TestMoreThanTenWritableIsCapped(t *testing.T) {
	f := goodFake()
	for i := 0; i < 11; i++ {
		f.writable = append(f.writable, map[string]any{"name": "db"})
	}
	r := Run(context.Background(), f, nil)
	if len(r.WritableNames) != 10 || r.WritableDatabases != 11 || !strings.Contains(r.Checks[3].Detail, "or more") {
		t.Fatalf("%+v", r)
	}
}

func TestWritableListFailureIsSkippedNotFailed(t *testing.T) {
	f := goodFake()
	f.deny["access:writable"] = true
	r := Run(context.Background(), f, nil)
	if !r.Ready || state(r, CheckWrite) != Skipped {
		t.Fatalf("%+v", r)
	}
}

func TestConnectFailureStopsAndNeverEchoesDriverText(t *testing.T) {
	err := errors.New(`login failed for user 'sa' at host db-prod-01.corp.example password=Hunter2`)
	r := Run(context.Background(), nil, err)
	if r.Reached || r.Verdict != VerdictUnreadable || len(r.Checks) != 1 {
		t.Fatalf("%+v", r)
	}
	d := r.Checks[0].Detail
	for _, bad := range []string{"sa", "db-prod", "Hunter2", "corp"} {
		if strings.Contains(d, bad) && bad != "sa" {
			t.Fatalf("detail leaks %q: %s", bad, d)
		}
	}
	if !strings.Contains(d, "refused this login") {
		t.Fatalf("detail %q", d)
	}
}

func TestConnectFailureClasses(t *testing.T) {
	cases := map[string]string{
		"x509: certificate signed by unknown authority": "secure connection",
		"dial tcp: lookup nowhere: no such host":        "could not be reached",
		"i/o timeout":                                   "could not be reached",
		"something odd":                                 "Could not connect",
	}
	for in, want := range cases {
		r := Run(context.Background(), nil, errors.New(in))
		if !strings.Contains(r.Checks[0].Detail, want) {
			t.Errorf("%q -> %q", in, r.Checks[0].Detail)
		}
	}
}

func TestLoginNameIsEscapedInTheScript(t *testing.T) {
	f := goodFake()
	f.who["login_name"] = "evil]; DROP LOGIN x;--\nnext"
	f.who["view_state"] = int64(0)
	r := Run(context.Background(), f, nil)
	if !strings.Contains(r.Script, "[evil]]; DROP LOGIN x;--next]") || strings.Count(r.Script, "\n") != 2 {
		t.Fatalf("script:\n%q", r.Script)
	}
}

func TestEmptyLoginNameGetsPlaceholder(t *testing.T) {
	if got := bracket("  \n "); got != "[<login name>]" {
		t.Fatal(got)
	}
}

func TestOwnDetailsUnreadableStopsEarly(t *testing.T) {
	f := goodFake()
	f.whoErr = errors.New("boom")
	r := Run(context.Background(), f, nil)
	if !r.Reached || r.Ready || r.Verdict != VerdictUnreadable || len(r.Checks) != 1 {
		t.Fatalf("%+v", r)
	}
}
