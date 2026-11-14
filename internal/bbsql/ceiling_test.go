package bbsql

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ceilingFake answers the two fixed ceiling control queries and records every
// text sent; any other text is a test failure.
type ceilingFake struct {
	role     []Row
	perms    []Row
	roleErr  error
	permsErr error
	sent     []string
}

func (f *ceilingFake) Query(_ context.Context, text string) ([]Row, error) {
	f.sent = append(f.sent, text)
	switch text {
	case Statements["bb:ceiling_role"]:
		return f.role, f.roleErr
	case Statements["bb:ceiling_perms"]:
		return f.perms, f.permsErr
	}
	return nil, errors.New("ceilingFake: unexpected query text")
}

// ceilingRoleNames pins the fixed server roles the C8 ceiling must refuse. It
// is written out here (not derived from ceilingRoles) so dropping a role from
// the Go check or from bb:ceiling_role fails a test.
var ceilingRoleNames = []string{
	"sysadmin", "securityadmin", "serveradmin", "setupadmin",
	"processadmin", "dbcreator", "bulkadmin", "diskadmin",
}

// noRoles is the role row of a login in no privileged fixed server role.
func noRoles() Row {
	r := Row{}
	for _, name := range ceilingRoleNames {
		r[name] = int64(0)
	}
	return r
}

// minimalCeiling models the granted virtual account: no privileged role, only
// CONNECT SQL and VIEW SERVER PERFORMANCE STATE.
func minimalCeiling() *ceilingFake {
	return &ceilingFake{
		role:  []Row{noRoles()},
		perms: []Row{{"permission_name": "CONNECT SQL"}, {"permission_name": "VIEW SERVER PERFORMANCE STATE"}},
	}
}

func assertTooManyRights(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrTooManyRights) {
		t.Fatalf("want ErrTooManyRights, got %v", err)
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTooManyRights {
		t.Fatalf("want fixed code %q, got %v", CodeTooManyRights, err)
	}
	if e.Message != "this login has more rights than the Blackbox needs" {
		t.Fatalf("want the plain ceiling reason, got %q", e.Message)
	}
}

// TestCeilingAllowsMinimal: exactly {CONNECT SQL, VIEW SERVER PERFORMANCE
// STATE}, no privileged role -> ok, with only the two allowlisted control queries.
func TestCeilingAllowsMinimal(t *testing.T) {
	f := minimalCeiling()
	if err := CheckCeiling(f); err != nil {
		t.Fatalf("minimal login refused: %v", err)
	}
	if len(f.sent) != 2 {
		t.Fatalf("want 2 control queries per connect, got %d", len(f.sent))
	}
	for _, text := range f.sent {
		if !IsAllowed(text) {
			t.Fatalf("non-allowlisted ceiling query sent: %q", text)
		}
	}
	empty := minimalCeiling()
	empty.perms = nil // the empty set is a subset of the ceiling too
	if err := CheckCeiling(empty); err != nil {
		t.Fatalf("empty permission set refused: %v", err)
	}
}

// roleWith is noRoles with one membership flag set.
func roleWith(name string, flag int64) Row {
	r := noRoles()
	r[name] = flag
	return r
}

func TestCeilingRefusesSysadmin(t *testing.T) {
	f := minimalCeiling()
	f.role = []Row{roleWith("sysadmin", 1)}
	assertTooManyRights(t, CheckCeiling(f))
}

func TestCeilingRefusesSecurityadmin(t *testing.T) {
	f := minimalCeiling()
	f.role = []Row{roleWith("securityadmin", 1)}
	assertTooManyRights(t, CheckCeiling(f))
}

// TestCeilingRefusesEveryPrivilegedRole: each fixed server role that carries
// more rights than the Blackbox needs refuses the login on its own, even with
// an in-ceiling permission set. serveradmin/setupadmin/processadmin/dbcreator/
// bulkadmin/diskadmin are not covered by the permission query (their rights
// come from role membership, not a direct grant), so the role flags are the
// only gate for them.
func TestCeilingRefusesEveryPrivilegedRole(t *testing.T) {
	for _, role := range ceilingRoleNames {
		t.Run(role, func(t *testing.T) {
			f := minimalCeiling()
			f.perms = nil // nothing outside the ceiling was granted directly
			f.role = []Row{roleWith(role, 1)}
			assertTooManyRights(t, CheckCeiling(f))
			// The refusal must come from the role flag alone: no permission row
			// was returned, so only the membership check can have refused.
			if len(f.sent) != 1 {
				t.Fatalf("want the role query only, got %d sends", len(f.sent))
			}
			// A non-zero flag other than 1 is still a refusal (never "0 means ok"
			// by equality on a specific value).
			other := minimalCeiling()
			other.perms = nil
			other.role = []Row{roleWith(role, 2)}
			assertTooManyRights(t, CheckCeiling(other))
		})
	}
}

// TestCeilingAllowsOnlyAllowedPerms: exactly the four allowed permissions and
// no privileged role membership is the widest login the Blackbox accepts.
func TestCeilingAllowsOnlyAllowedPerms(t *testing.T) {
	f := minimalCeiling()
	f.perms = []Row{
		{"permission_name": "CONNECT SQL"},
		{"permission_name": "VIEW ANY DATABASE"},
		{"permission_name": "VIEW SERVER STATE"},
		{"permission_name": "VIEW SERVER PERFORMANCE STATE"},
	}
	if err := CheckCeiling(f); err != nil {
		t.Fatalf("the four allowed permissions with no roles were refused: %v", err)
	}
	if len(f.sent) != 2 {
		t.Fatalf("want 2 control queries per connect, got %d", len(f.sent))
	}
}

// TestCeilingPermsUsesServerPrincipal pins the C8 bypass fix: sys.server_permissions.
// grantee_principal_id is a SERVER principal id (sys.server_principals), so the
// predicate must resolve the current login with SUSER_ID(). DATABASE_PRINCIPAL_ID()
// returns a DATABASE principal id: with the C14 grant (login only, no user in the
// database, so the session runs as guest = database principal 2) it matched the
// public server role (server principal 2) instead of the login, hiding a
// GRANT CONTROL SERVER to that login and letting an over-privileged login pass.
func TestCeilingPermsUsesServerPrincipal(t *testing.T) {
	perms := Statements["bb:ceiling_perms"]
	if !strings.Contains(perms, "WHERE grantee_principal_id = SUSER_ID()") {
		t.Errorf("bb:ceiling_perms must filter on the login's SERVER principal id via SUSER_ID(): %q", perms)
	}
	if strings.Contains(strings.ToUpper(perms), "DATABASE_PRINCIPAL_ID") {
		t.Error("bb:ceiling_perms must not use DATABASE_PRINCIPAL_ID(): a database principal id is not a server principal id (C8 bypass)")
	}
	// The role query is server-scoped too, and stays one FROM-less SELECT.
	role := Statements["bb:ceiling_role"]
	if !strings.Contains(role, "IS_SRVROLEMEMBER(") {
		t.Error("bb:ceiling_role must use server-scoped IS_SRVROLEMEMBER")
	}
	if strings.Contains(strings.ToUpper(role), "IS_MEMBER(") {
		t.Error("bb:ceiling_role must not use database-scoped IS_MEMBER")
	}
}

// TestCeilingRoleColumnsMatchGo proves the SQL and the Go check cover the same
// eight roles: every selected column is asserted, and every asserted role is
// selected. A role added to one side only is a silent ceiling gap.
func TestCeilingRoleColumnsMatchGo(t *testing.T) {
	cols := outputColumns(t, Statements["bb:ceiling_role"])
	if len(cols) != len(ceilingRoleNames) {
		t.Fatalf("bb:ceiling_role selects %d columns, want %d", len(cols), len(ceilingRoleNames))
	}
	selected := map[string]bool{}
	for _, c := range cols {
		selected[strings.ToLower(c)] = true
	}
	for _, role := range ceilingRoleNames {
		if !selected[role] {
			t.Errorf("bb:ceiling_role does not select role %q", role)
		}
		if !strings.Contains(Statements["bb:ceiling_role"], "IS_SRVROLEMEMBER(N'"+role+"') AS "+role) {
			t.Errorf("bb:ceiling_role missing IS_SRVROLEMEMBER flag for %q", role)
		}
	}
	if len(ceilingRoles) != len(ceilingRoleNames) {
		t.Fatalf("CheckCeiling asserts %d roles, want %d", len(ceilingRoles), len(ceilingRoleNames))
	}
	for i, role := range ceilingRoleNames {
		if ceilingRoles[i] != role {
			t.Errorf("ceilingRoles[%d] = %q, want %q", i, ceilingRoles[i], role)
		}
	}
}

func TestCeilingRefusesControlServer(t *testing.T) {
	f := minimalCeiling()
	// bb:ceiling_perms returns only rights outside the ceiling.
	f.perms = []Row{{"permission_name": "CONTROL SERVER"}}
	assertTooManyRights(t, CheckCeiling(f))
}

func TestCeilingRefusesExtraPermission(t *testing.T) {
	f := minimalCeiling()
	f.perms = []Row{{"permission_name": "ALTER ANY DATABASE"}}
	assertTooManyRights(t, CheckCeiling(f))
	// An extra right is refused even when an allowed right rides along.
	mixed := minimalCeiling()
	mixed.perms = []Row{{"permission_name": "CONTROL SERVER"}, {"permission_name": "CONNECT SQL"}}
	assertTooManyRights(t, CheckCeiling(mixed))
}

// ceilingFailCase is one unreadable-result mutation for TestCeilingFailsClosed.
type ceilingFailCase struct {
	name string
	mut  func(*ceilingFake)
}

// TestCeilingFailsClosed: unreadable or failed control results never pass and
// never surface driver text.
func TestCeilingFailsClosed(t *testing.T) {
	cases := []ceilingFailCase{
		{"role-query-error", func(f *ceilingFake) { f.roleErr = errors.New("driver: reset by peer SECRET") }},
		{"perms-query-error", func(f *ceilingFake) { f.permsErr = errors.New("driver: reset by peer SECRET") }},
		{"no-role-row", func(f *ceilingFake) { f.role = nil }},
		{"null-role-flag", func(f *ceilingFake) {
			r := noRoles()
			r["sysadmin"] = nil
			f.role = []Row{r}
		}},
		{"bad-permission-value", func(f *ceilingFake) { f.perms = []Row{{"permission_name": nil}} }},
	}
	// A NULL or absent flag for ANY of the eight roles is unreadable, never 0:
	// a role dropped from the result cannot widen the ceiling.
	for _, role := range ceilingRoleNames {
		cases = append(cases,
			ceilingFailCase{"null-flag-" + role, func(f *ceilingFake) {
				r := noRoles()
				r[role] = nil
				f.role = []Row{r}
			}},
			ceilingFailCase{"missing-column-" + role, func(f *ceilingFake) {
				r := noRoles()
				delete(r, role)
				f.role = []Row{r}
			}},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := minimalCeiling()
			tc.mut(f)
			err := CheckCeiling(f)
			if err == nil {
				t.Fatal("unreadable ceiling result passed")
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("want a fixed-code error, got %v", err)
			}
			if strings.Contains(e.Message, "SECRET") || strings.Contains(e.Message, "driver") {
				t.Fatalf("driver text surfaced: %q", e.Message)
			}
		})
	}
}

// TestCeilingQueriesLintClean: both control statements are allowlisted and
// pass the C2 lint; the perms query filters to rights OUTSIDE the ceiling so
// the C6 row cap can never hide an extra right, and resolves the grantee with
// the SERVER principal id (SUSER_ID) so the C14 guest session cannot hide a
// grant made to the login.
func TestCeilingQueriesLintClean(t *testing.T) {
	for _, id := range []string{"bb:ceiling_role", "bb:ceiling_perms"} {
		text, ok := Statement(id)
		if !ok || !IsAllowed(text) {
			t.Fatalf("%s missing from the allowlist", id)
		}
		if !strings.HasPrefix(text, setPrefix) {
			t.Errorf("%s: missing the C3 SET prefix", id)
		}
		if n := strings.Count(strings.ToUpper(text), "SELECT"); n != 1 {
			t.Errorf("%s: want exactly 1 SELECT, got %d", id, n)
		}
		if tag := "/* unravis:" + id + " */"; !strings.Contains(text, tag) {
			t.Errorf("%s: missing tag %q", id, tag)
		}
		if tok := bannedToken(text); tok != "" {
			t.Errorf("%s: banned token %q", id, tok)
		}
	}
	perms := Statements["bb:ceiling_perms"]
	if !strings.Contains(perms, "SELECT TOP 2 permission_name") {
		t.Error("bb:ceiling_perms must keep the TOP 2 reader-cap shape")
	}
	if !strings.Contains(perms, "FROM sys.server_permissions") {
		t.Error("bb:ceiling_perms must read sys.server_permissions")
	}
	if !strings.Contains(perms, "WHERE grantee_principal_id = SUSER_ID()") {
		t.Error("bb:ceiling_perms must match the grantee on SUSER_ID() (server principal id)")
	}
	if strings.Contains(strings.ToUpper(perms), "DATABASE_PRINCIPAL_ID") {
		t.Error("bb:ceiling_perms must not match the grantee on a database principal id")
	}
	if !strings.Contains(perms, "NOT IN") {
		t.Error("bb:ceiling_perms must filter to permissions outside the ceiling")
	}
	for _, lit := range []string{"N'CONNECT SQL'", "N'VIEW ANY DATABASE'", "N'VIEW SERVER STATE'", "N'VIEW SERVER PERFORMANCE STATE'"} {
		if !strings.Contains(perms, lit) {
			t.Errorf("bb:ceiling_perms missing ceiling literal %s", lit)
		}
	}
	role := Statements["bb:ceiling_role"]
	if strings.Contains(strings.ToUpper(role), "FROM") {
		t.Error("bb:ceiling_role must stay one FROM-less flag SELECT")
	}
	for _, name := range ceilingRoleNames {
		if !strings.Contains(role, "IS_SRVROLEMEMBER(N'"+name+"') AS "+name) {
			t.Errorf("bb:ceiling_role missing membership flag for %s", name)
		}
	}
}
