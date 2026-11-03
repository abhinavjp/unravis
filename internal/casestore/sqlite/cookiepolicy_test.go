package sqlite

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func cookieMeta(t *testing.T, c *Case, k string) (string, bool) {
	t.Helper()
	v, found, err := metaValue(context.Background(), c.db, k)
	if err != nil {
		t.Fatal(err)
	}
	return v, found
}

func cookieRows(t *testing.T, c *Case) (allow, key string) {
	t.Helper()
	allow, _ = cookieMeta(t, c, cookieAllowKey)
	key, _ = cookieMeta(t, c, cookieKeyKey)
	return allow, key
}

func cookieCount(t *testing.T, c *Case) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM meta WHERE k LIKE 'cookie.%'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCookiePolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookies").ID)
	got, err := c.SetCookieAllowlist(ctx, []string{"Theme", "sid"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sid", "theme"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("set returned %v, want %v", got, want)
	}
	pol, err := c.CookiePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pol.Allowed, want) || len(pol.HMACKey) != casestore.CookieKeyBytes {
		t.Fatalf("policy = %v with a %d-byte key", pol.Allowed, len(pol.HMACKey))
	}
	allow, key := cookieRows(t, c)
	var arr []string
	if err := json.Unmarshal([]byte(allow), &arr); err != nil || !reflect.DeepEqual(arr, want) {
		t.Fatalf("allowlist row %q is not the JSON array of the names", allow)
	}
	if len(key) != 64 || key != strings.ToLower(key) {
		t.Fatalf("key row has length %d, want 64 lowercase hex characters", len(key))
	}
	if b, err := hex.DecodeString(key); err != nil || !bytes.Equal(b, pol.HMACKey) {
		t.Fatal("key row does not decode to the policy key")
	}
}

func TestCookiePolicySurvivesReopen(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	id := mustCreate(t, s, "cookies").ID
	c := mustOpen(t, s, id)
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid", "theme"}); err != nil {
		t.Fatal(err)
	}
	first, err := c.CookiePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, s, id)
	again, err := c2.CookiePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Allowed, again.Allowed) || !bytes.Equal(first.HMACKey, again.HMACKey) {
		t.Fatal("policy changed across a reopen")
	}
}

func TestCookieKeyCreatedOnceAndKept(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookies").ID)
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	_, key1 := cookieRows(t, c)
	if _, err := c.SetCookieAllowlist(ctx, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if _, key2 := cookieRows(t, c); key2 != key1 {
		t.Fatal("a second set replaced the key")
	}
	got, err := c.SetCookieAllowlist(ctx, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("clear = %v, %v", got, err)
	}
	allow, key3 := cookieRows(t, c)
	if allow != "[]" {
		t.Fatalf("cleared allowlist row = %q, want []", allow)
	}
	if _, found := cookieMeta(t, c, cookieKeyKey); !found || key3 != key1 {
		t.Fatal("clearing removed or replaced the key row")
	}
	pol, err := c.CookiePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pol.Allowed != nil || pol.HMACKey != nil {
		t.Fatalf("cleared policy is not the zero policy: %+v", pol)
	}
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	if pol, _ = c.CookiePolicy(ctx); hex.EncodeToString(pol.HMACKey) != key1 {
		t.Fatal("setting again did not restore the identical key")
	}
	// The no-op rule: the same list again writes nothing.
	beforeAllow, beforeKey := cookieRows(t, c)
	if _, err := c.SetCookieAllowlist(ctx, []string{"SID"}); err != nil {
		t.Fatal(err)
	}
	if a, k := cookieRows(t, c); a != beforeAllow || k != beforeKey {
		t.Fatal("an unchanged call altered the rows")
	}
	// Clearing an already empty list is also a no-op and creates no rows.
	c2 := mustOpen(t, s, mustCreate(t, s, "empty").ID)
	if _, err := c2.SetCookieAllowlist(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := cookieCount(t, c2); n != 0 {
		t.Fatalf("clearing an empty list wrote %d cookie rows", n)
	}
}

func TestCookiePolicyUnsetWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookies").ID)
	pol, err := c.CookiePolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pol.Allowed != nil || pol.HMACKey != nil {
		t.Fatalf("unset policy = %+v", pol)
	}
	if n := cookieCount(t, c); n != 0 {
		t.Fatalf("reading the policy wrote %d cookie rows", n)
	}
}

func TestCookiePolicyRefusals(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "cookies")
	c := mustOpen(t, s, info.ID)
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	beforeAllow, beforeKey := cookieRows(t, c)
	same := func(when string) {
		t.Helper()
		if a, k := cookieRows(t, c); a != beforeAllow || k != beforeKey {
			t.Fatalf("%s changed the stored rows", when)
		}
	}
	if _, err := c.SetCookieAllowlist(ctx, []string{"bad name"}); !errors.Is(err, casestore.ErrBadCookieName) {
		t.Fatalf("bad name = %v, want ErrBadCookieName", err)
	}
	same("a bad name")
	if _, err := c.SetCookieAllowlist(ctx, []string{"a", "b", "c", "d", "e"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Fatalf("five names = %v, want ErrCapExceeded", err)
	}
	same("five names")
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetCookieAllowlist(ctx, []string{"other"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Fatalf("closed case = %v, want ErrCaseClosed", err)
	}
	same("a closed case")
}

func TestCookiePolicyCorruptRows(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookies").ID)
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"zz", hex.EncodeToString(bytes.Repeat([]byte{0xab}, 10))} {
		if _, err := c.db.Exec("UPDATE meta SET v=? WHERE k=?", bad, cookieKeyKey); err != nil {
			t.Fatal(err)
		}
		_, err := c.CookiePolicy(ctx)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("key %q: err = %v, want ErrInvalid", bad, err)
		}
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error text contains the stored key value: %v", err)
		}
	}
	if _, err := c.db.Exec("UPDATE meta SET v=? WHERE k=?", "Not-JSON", cookieAllowKey); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CookiePolicy(ctx); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "Not-JSON") {
		t.Fatalf("corrupt allowlist: err = %v", err)
	}
	if _, err := c.db.Exec("UPDATE meta SET v=? WHERE k=?", `["Bad Name"]`, cookieAllowKey); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CookiePolicy(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uncleanable allowlist: err = %v", err)
	}
}

func TestCookieKeyNotInManifest(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookies").ID)
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	_, key := cookieRows(t, c)
	info, err := json.Marshal(c.Info())
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	man, err := json.Marshal(c.man)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	disk, err := readManifest(c.dir, c.s.sealer(c.dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	diskJSON, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(c.dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"info": info, "manifest": man, "manifest on disk": diskJSON, "manifest file": raw} {
		if bytes.Contains(b, []byte(key)) || bytes.Contains(b, []byte(strings.ToUpper(key))) {
			t.Fatalf("the key hex appears in the %s", name)
		}
	}
}
