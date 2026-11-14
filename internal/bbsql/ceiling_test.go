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

// minimalCeiling models the granted virtual account: no privileged role, only
// CONNECT SQL and VIEW SERVER PERFORMANCE STATE.
func minimalCeiling() *ceilingFake {
	return &ceilingFake{
		role:  []Row{{"sysadmin": int64(0), "securityadmin": int64(0)}},
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
// STATE}, not sysadmin -> ok, with only the two allowlisted control queries.
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

func TestCeilingRefusesSysadmin(t *testing.T) {
	f := minimalCeiling()
	f.role = []Row{{"sysadmin": int64(1), "securityadmin": int64(0)}}
	assertTooManyRights(t, CheckCeiling(f))
}

func TestCeilingRefusesSecurityadmin(t *testing.T) {
	f := minimalCeiling()
	f.role = []Row{{"sysadmin": int64(0), "securityadmin": int64(1)}}
	assertTooManyRights(t, CheckCeiling(f))
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

// TestCeilingFailsClosed: unreadable or failed control results never pass and
// never surface driver text.
func TestCeilingFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ceilingFake)
	}{
		{"role-query-error", func(f *ceilingFake) { f.roleErr = errors.New("driver: reset by peer SECRET") }},
		{"perms-query-error", func(f *ceilingFake) { f.permsErr = errors.New("driver: reset by peer SECRET") }},
		{"no-role-row", func(f *ceilingFake) { f.role = nil }},
		{"null-role-flag", func(f *ceilingFake) { f.role = []Row{{"sysadmin": nil, "securityadmin": int64(0)}} }},
		{"bad-permission-value", func(f *ceilingFake) { f.perms = []Row{{"permission_name": nil}} }},
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
// the C6 row cap can never hide an extra right.
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
	if !strings.Contains(perms, "NOT IN") {
		t.Error("bb:ceiling_perms must filter to permissions outside the ceiling")
	}
	for _, lit := range []string{"N'CONNECT SQL'", "N'VIEW ANY DATABASE'", "N'VIEW SERVER STATE'", "N'VIEW SERVER PERFORMANCE STATE'"} {
		if !strings.Contains(perms, lit) {
			t.Errorf("bb:ceiling_perms missing ceiling literal %s", lit)
		}
	}
}
