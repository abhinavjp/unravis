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

// ceilingRoleNames pins EVERY column of bb:ceiling_role: the eight privileged
// fixed-role membership flags plus the four C8 positive-proof failure flags
// (W9-01). All are 0-on-good, so the shared fakes (runner_test ceilingOK,
// budget_test budgetReplies) build a compliant row by zeroing this list. It is
// written out here (not derived from ceilingRoles) so dropping a column from
// the Go check or from bb:ceiling_role fails a test.
var ceilingRoleNames = []string{
	"sysadmin", "securityadmin", "serveradmin", "setupadmin",
	"processadmin", "dbcreator", "bulkadmin", "diskadmin",
	"principal_missing", "perm_empty", "perm_outside", "connect_missing",
}

// privilegedRoleNames are the eight fixed server roles whose membership alone
// refuses the login (the role-flag half of bb:ceiling_role).
var privilegedRoleNames = []string{
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
	if err := CheckCeiling(context.Background(), f); err != nil {
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
	if err := CheckCeiling(context.Background(), empty); err != nil {
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
	assertTooManyRights(t, CheckCeiling(context.Background(), f))
}

func TestCeilingRefusesSecurityadmin(t *testing.T) {
	f := minimalCeiling()
	f.role = []Row{roleWith("securityadmin", 1)}
	assertTooManyRights(t, CheckCeiling(context.Background(), f))
}

// TestCeilingRefusesEveryPrivilegedRole: each fixed server role that carries
// more rights than the Blackbox needs refuses the login on its own, even with
// an in-ceiling permission set. serveradmin/setupadmin/processadmin/dbcreator/
// bulkadmin/diskadmin are not covered by the permission query (their rights
// come from role membership, not a direct grant), so the role flags are the
// only gate for them.
func TestCeilingRefusesEveryPrivilegedRole(t *testing.T) {
	for _, role := range privilegedRoleNames {
		t.Run(role, func(t *testing.T) {
			f := minimalCeiling()
			f.perms = nil // nothing outside the ceiling was granted directly
			f.role = []Row{roleWith(role, 1)}
			assertTooManyRights(t, CheckCeiling(context.Background(), f))
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
			assertTooManyRights(t, CheckCeiling(context.Background(), other))
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
	if err := CheckCeiling(context.Background(), f); err != nil {
		t.Fatalf("the four allowed permissions with no roles were refused: %v", err)
	}
	if len(f.sent) != 2 {
		t.Fatalf("want 2 control queries per connect, got %d", len(f.sent))
	}
}

// TestCeilingPermsUsesEffectiveRights pins the W9-01 fix: the permission proof
// reads sys.fn_my_permissions(NULL, N'SERVER') — the EFFECTIVE server rights of
// the current security context — not the direct-grant catalog. A right granted
// to a custom/nested server role, a Windows group, public or a SQL2022
// ##MS_*## role belongs to another principal id and was invisible to the old
// grantee_principal_id = SUSER_ID() predicate on sys.server_permissions. The
// function also subtracts DENYs, so a protective DENY is never reported as an
// excess right (no owner is told to remove a DENY to pass).
func TestCeilingPermsUsesEffectiveRights(t *testing.T) {
	for _, id := range []string{"bb:ceiling_perms", "bb:ceiling_role"} {
		text := Statements[id]
		if !strings.Contains(text, "sys.fn_my_permissions(NULL, N'SERVER')") {
			t.Errorf("%s must read the effective-permission function: %q", id, text)
		}
		if strings.Contains(text, "sys.server_permissions") {
			t.Errorf("%s must not read the direct-grant catalog (inherited rights are invisible there)", id)
		}
		if strings.Contains(strings.ToUpper(text), "DATABASE_PRINCIPAL_ID") {
			t.Errorf("%s must not use a database principal id", id)
		}
	}
	// The role query keeps the server-scoped membership flags.
	role := Statements["bb:ceiling_role"]
	if !strings.Contains(role, "IS_SRVROLEMEMBER(") {
		t.Error("bb:ceiling_role must use server-scoped IS_SRVROLEMEMBER")
	}
	if strings.Contains(strings.ToUpper(role), "IS_MEMBER(") {
		t.Error("bb:ceiling_role must not use database-scoped IS_MEMBER")
	}
	// The positive proof: an empty or unresolved result must never mean clean.
	for _, flag := range []string{"principal_missing", "perm_empty", "perm_outside", "connect_missing"} {
		if !strings.Contains(role, "AS "+flag) {
			t.Errorf("bb:ceiling_role missing the positive-proof flag %q", flag)
		}
	}
	if !strings.Contains(role, "SUSER_ID() IS NULL") {
		t.Error("bb:ceiling_role must prove the principal resolved (SUSER_ID)")
	}
	if !strings.Contains(role, "COUNT(*) = 0") {
		t.Error("bb:ceiling_role must flag an empty effective-permission list")
	}
}

// TestCeilingRoleColumnsMatchGo proves the SQL and the Go check cover the same
// columns: every selected column is asserted, and every asserted column is
// selected. A column added to one side only is a silent ceiling gap. The eight
// role flags and the four proof flags are checked separately.
func TestCeilingRoleColumnsMatchGo(t *testing.T) {
	cols := outputColumns(t, Statements["bb:ceiling_role"])
	if len(cols) != len(ceilingRoleNames) {
		t.Fatalf("bb:ceiling_role selects %d columns, want %d", len(cols), len(ceilingRoleNames))
	}
	selected := map[string]bool{}
	for _, c := range cols {
		selected[strings.ToLower(c)] = true
	}
	for _, role := range privilegedRoleNames {
		if !selected[role] {
			t.Errorf("bb:ceiling_role does not select role %q", role)
		}
		if !strings.Contains(Statements["bb:ceiling_role"], "IS_SRVROLEMEMBER(N'"+role+"') AS "+role) {
			t.Errorf("bb:ceiling_role missing IS_SRVROLEMEMBER flag for %q", role)
		}
	}
	for _, flag := range ceilingProofFlags {
		if !selected[flag] {
			t.Errorf("bb:ceiling_role does not select proof flag %q", flag)
		}
	}
	if len(ceilingRoles) != len(privilegedRoleNames) {
		t.Fatalf("CheckCeiling asserts %d roles, want %d", len(ceilingRoles), len(privilegedRoleNames))
	}
	for i, role := range privilegedRoleNames {
		if ceilingRoles[i] != role {
			t.Errorf("ceilingRoles[%d] = %q, want %q", i, ceilingRoles[i], role)
		}
	}
	if len(ceilingProofFlags) != 4 {
		t.Fatalf("CheckCeiling asserts %d proof flags, want 4", len(ceilingProofFlags))
	}
}

func TestCeilingRefusesControlServer(t *testing.T) {
	f := minimalCeiling()
	// bb:ceiling_perms returns only rights outside the ceiling.
	f.perms = []Row{{"permission_name": "CONTROL SERVER"}}
	assertTooManyRights(t, CheckCeiling(context.Background(), f))
}

func TestCeilingRefusesExtraPermission(t *testing.T) {
	f := minimalCeiling()
	f.perms = []Row{{"permission_name": "ALTER ANY DATABASE"}}
	assertTooManyRights(t, CheckCeiling(context.Background(), f))
	// An extra right is refused even when an allowed right rides along.
	mixed := minimalCeiling()
	mixed.perms = []Row{{"permission_name": "CONTROL SERVER"}, {"permission_name": "CONNECT SQL"}}
	assertTooManyRights(t, CheckCeiling(context.Background(), mixed))
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
			err := CheckCeiling(context.Background(), f)
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
// pass the restricted C2 grammar; the perms query filters to effective rights
// OUTSIDE the ceiling so the C6 row cap can never hide an extra right, and the
// role query carries the positive readable-proof aggregates (W9-01).
func TestCeilingQueriesLintClean(t *testing.T) {
	for _, id := range []string{"bb:ceiling_role", "bb:ceiling_perms"} {
		text, ok := Statement(id)
		if !ok || !IsAllowed(text) {
			t.Fatalf("%s missing from the allowlist", id)
		}
		if err := lintStatement(id, text); err != nil {
			t.Errorf("%s fails the C2 grammar: %v", id, err)
		}
		if tok := bannedToken(text); tok != "" {
			t.Errorf("%s: banned token %q", id, tok)
		}
	}
	perms := Statements["bb:ceiling_perms"]
	if !strings.Contains(perms, "SELECT TOP 2 permission_name") {
		t.Error("bb:ceiling_perms must keep the TOP 2 reader-cap shape")
	}
	if !strings.Contains(perms, "FROM sys.fn_my_permissions(NULL, N'SERVER')") {
		t.Error("bb:ceiling_perms must read the effective-permission function")
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
	for _, name := range privilegedRoleNames {
		if !strings.Contains(role, "IS_SRVROLEMEMBER(N'"+name+"') AS "+name) {
			t.Errorf("bb:ceiling_role missing membership flag for %s", name)
		}
	}
}

// TestCeilingProofFailsClosed (W9-01): a non-zero or unreadable positive-proof
// flag never passes. An empty effective list, an unresolved principal or a
// missing CONNECT SQL is NOT a clean login.
func TestCeilingProofFailsClosed(t *testing.T) {
	for _, flag := range ceilingProofFlags {
		for _, val := range []any{int64(1), int64(2), nil} {
			f := minimalCeiling()
			r := noRoles()
			if val == nil {
				delete(r, flag)
			} else {
				r[flag] = val
			}
			f.role = []Row{r}
			err := CheckCeiling(context.Background(), f)
			if err == nil {
				t.Fatalf("proof flag %s=%v passed", flag, val)
			}
			if !IsCeilingFailure(err) {
				t.Fatalf("proof flag %s=%v: error lost its C8 origin: %v", flag, val, err)
			}
			if flag == "perm_outside" && val != nil {
				assertTooManyRights(t, err)
			}
		}
	}
	// No role row at all is unreadable, never clean.
	f := minimalCeiling()
	f.role = nil
	if err := CheckCeiling(context.Background(), f); err == nil || !IsCeilingFailure(err) {
		t.Fatalf("missing role row: %v", err)
	}
}

// TestCeilingFailureKeepsOrigin (W9-03): EVERY failed proof keeps its C8 origin
// independently of the public code, so the runner can latch immediately. The
// unreadable case keeps the generic public code (no new public surface) while
// IsCeilingFailure still reports true; Classify still sees the fixed inner
// error and no driver text escapes.
func TestCeilingFailureKeepsOrigin(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ceilingFake)
		code string
	}{
		{"unreadable", func(f *ceilingFake) { f.role = nil }, CodeFailed},
		{"role-query-error", func(f *ceilingFake) { f.roleErr = errors.New("driver: SECRET reset") }, CodeQuery},
		{"perms-query-error", func(f *ceilingFake) { f.permsErr = errors.New("driver: SECRET reset") }, CodeQuery},
		{"too-many-rights", func(f *ceilingFake) { f.role = []Row{roleWith("sysadmin", 1)} }, CodeTooManyRights},
		{"outside-right", func(f *ceilingFake) { f.perms = []Row{{"permission_name": "CONTROL SERVER"}} }, CodeTooManyRights},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := minimalCeiling()
			tc.mut(f)
			err := CheckCeiling(context.Background(), f)
			if err == nil {
				t.Fatal("failed proof passed")
			}
			if !IsCeilingFailure(err) {
				t.Fatalf("C8 origin lost: %v", err)
			}
			e := Classify(err)
			if e.Code != tc.code {
				t.Fatalf("public code = %q, want %q", e.Code, tc.code)
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(e.Message, "SECRET") {
				t.Fatalf("driver text surfaced: %q", err.Error())
			}
		})
	}
	// A non-ceiling error is not mislabelled as a ceiling failure.
	if IsCeilingFailure(errors.New("plain")) || IsCeilingFailure(nil) {
		t.Fatal("IsCeilingFailure matched a non-ceiling error")
	}
	if IsCeilingFailure(&Error{CodeFailed, "x"}) {
		t.Fatal("a bare fixed error without the ceiling wrapper matched")
	}
}
