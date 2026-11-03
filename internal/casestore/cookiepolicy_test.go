package casestore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// openCookieCase returns an open handle on a fresh in-memory case, already
// asserted to implement casestore.CookiePolicyStore and closed at test end.
func openCookieCase(t *testing.T) casestore.Case {
	t.Helper()
	ctx := context.Background()
	st := casestore.NewMemory()
	info, err := st.Create(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(casestore.CookiePolicyStore); !ok {
		t.Fatalf("%T does not implement casestore.CookiePolicyStore", c)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCleanCookieNames(t *testing.T) {
	if got, err := casestore.CleanCookieNames([]string{" SID ", "sid", "Theme"}); err != nil || !equalStrings(got, []string{"sid", "theme"}) {
		t.Fatalf("CleanCookieNames = %v %v", got, err)
	}

	if got, err := casestore.CleanCookieNames(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil input = %v %v, want an empty non-nil slice", got, err)
	}

	bad := [][]string{
		{"a b"},
		{"x;y"},
		{"é"},
		{strings.Repeat("a", 49)},
		{"cause"},
		{"Proof"},
		{"due-to"},
	}
	for _, names := range bad {
		if _, err := casestore.CleanCookieNames(names); !errors.Is(err, casestore.ErrBadCookieName) {
			t.Errorf("%v: %v, want ErrBadCookieName", names, err)
		}
	}

	if _, err := casestore.CleanCookieNames([]string{"a", "b", "c", "d", "e"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("five names: %v, want ErrCapExceeded", err)
	}

	if got, err := casestore.CleanCookieNames([]string{"a", "b", "c", "d"}); err != nil || len(got) != 4 {
		t.Errorf("four names: %v %v", got, err)
	}

	long := strings.Repeat("a", casestore.MaxCookieNameLen)
	if got, err := casestore.CleanCookieNames([]string{long}); err != nil || len(got) != 1 || got[0] != long {
		t.Errorf("48-char name: %v %v", got, err)
	}
}

func TestCookiePolicyZeroWhenUnset(t *testing.T) {
	c := openCookieCase(t)
	cp := c.(casestore.CookiePolicyStore)
	ctx := context.Background()

	pol, err := cp.CookiePolicy(ctx)
	if err != nil || pol.Allowed != nil || pol.HMACKey != nil {
		t.Fatalf("unset CookiePolicy = %+v %v, want the zero value", pol, err)
	}

	if _, err := cp.SetCookieAllowlist(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || pol.HMACKey != nil {
		t.Fatalf("after an empty SetCookieAllowlist: %+v %v, want a nil key", pol, err)
	}
}

func TestSetCookieAllowlistCreatesKeyOnce(t *testing.T) {
	c := openCookieCase(t)
	cp := c.(casestore.CookiePolicyStore)
	ctx := context.Background()

	got, err := cp.SetCookieAllowlist(ctx, []string{"Theme", "sid"})
	want := []string{"sid", "theme"}
	if err != nil || !equalStrings(got, want) {
		t.Fatalf("SetCookieAllowlist = %v %v, want %v", got, err, want)
	}
	pol, err := cp.CookiePolicy(ctx)
	if err != nil || len(pol.HMACKey) != casestore.CookieKeyBytes || !equalStrings(pol.Allowed, want) {
		t.Fatalf("CookiePolicy = %+v %v", pol, err)
	}
	key := append([]byte(nil), pol.HMACKey...)

	if _, err := cp.SetCookieAllowlist(ctx, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || string(pol.HMACKey) != string(key) {
		t.Fatalf("a second set changed the key: %+v %v", pol, err)
	}

	cleared, err := cp.SetCookieAllowlist(ctx, nil)
	if err != nil || len(cleared) != 0 {
		t.Fatalf("clearing the allowlist: %v %v", cleared, err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || pol.Allowed != nil || pol.HMACKey != nil {
		t.Fatalf("cleared CookiePolicy = %+v %v, want the zero value", pol, err)
	}

	if _, err := cp.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || string(pol.HMACKey) != string(key) {
		t.Fatalf("the restored key differs: %+v %v", pol, err)
	}
}

func TestCookiePolicyReturnsCopies(t *testing.T) {
	c := openCookieCase(t)
	cp := c.(casestore.CookiePolicyStore)
	ctx := context.Background()

	if _, err := cp.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	pol, err := cp.CookiePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keyByte := pol.HMACKey[0]
	pol.Allowed[0] = "mutated"
	pol.HMACKey[0] ^= 0xFF

	again, err := cp.CookiePolicy(ctx)
	if err != nil || again.Allowed[0] != "sid" {
		t.Errorf("mutating the returned Allowed leaked into the store: %+v %v", again, err)
	}
	if again.HMACKey[0] != keyByte {
		t.Error("mutating the returned HMACKey leaked into the store")
	}
}

func TestSetCookieAllowlistRefusals(t *testing.T) {
	ctx := context.Background()
	st := casestore.NewMemory()
	info, err := st.Create(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	cp, ok := c.(casestore.CookiePolicyStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.CookiePolicyStore", c)
	}

	if _, err := cp.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}

	// A bad name is refused and leaves the list unchanged.
	if _, err := cp.SetCookieAllowlist(ctx, []string{"x;y"}); !errors.Is(err, casestore.ErrBadCookieName) {
		t.Errorf("bad name: %v, want ErrBadCookieName", err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || !equalStrings(pol.Allowed, []string{"sid"}) {
		t.Errorf("a bad-name refusal changed the list: %+v %v", pol, err)
	}

	// A cancelled context is refused and leaves the list unchanged.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := cp.SetCookieAllowlist(cctx, []string{"theme"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v, want context.Canceled", err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || !equalStrings(pol.Allowed, []string{"sid"}) {
		t.Errorf("a cancelled write changed the list: %+v %v", pol, err)
	}

	// A closed case is refused and leaves the list unchanged.
	if err := st.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.SetCookieAllowlist(ctx, []string{"other"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("closed case: %v, want ErrCaseClosed", err)
	}
	if pol, err := cp.CookiePolicy(ctx); err != nil || !equalStrings(pol.Allowed, []string{"sid"}) {
		t.Errorf("a closed-case refusal changed the list: %+v %v", pol, err)
	}
}

func TestNewCookieKey(t *testing.T) {
	a, err := casestore.NewCookieKey()
	if err != nil || len(a) != casestore.CookieKeyBytes {
		t.Fatalf("NewCookieKey = %v %v", a, err)
	}
	b, err := casestore.NewCookieKey()
	if err != nil || len(b) != casestore.CookieKeyBytes {
		t.Fatalf("NewCookieKey = %v %v", b, err)
	}
	if string(a) == string(b) {
		t.Error("two calls to NewCookieKey returned identical keys")
	}
}
