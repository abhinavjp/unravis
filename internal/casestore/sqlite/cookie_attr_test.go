package sqlite

import (
	"context"
	"errors"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

const cookieDigest = "0123456789abcdef0123456789abcdef"

// cookieImport imports one event carrying the given extra attr under the given
// allowlist and returns the case, the error and the stored event count.
func cookieImport(t *testing.T, label, key string, allowed map[string]bool) (*Case, error, int) {
	t.Helper()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "cookie attrs").ID)
	evs := tEvents(c, label, 1)
	evs[0].Attrs = map[string]string{"counter": "total", key: cookieDigest}
	meta := tMeta(label + ".csv")
	meta.AllowedAttrs = allowed
	_, err := c.Import(context.Background(), meta, tBody(label), tRun(evs))
	_, n, _ := counts(t, c)
	return c, err, n
}

func TestImportAcceptsCookieAttrWithWildcard(t *testing.T) {
	c, err, n := cookieImport(t, "ck", "cookie:sid", map[string]bool{"counter": true, "cookie:*": true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	got, err := c.Events(context.Background(), casestore.EventQuery{})
	if err != nil || len(got) != 1 {
		t.Fatalf("read back: %d events, err %v", len(got), err)
	}
	if got[0].Attrs["cookie:sid"] != cookieDigest {
		t.Errorf("cookie:sid = %q, want the stored digest", got[0].Attrs["cookie:sid"])
	}
}

func TestImportRefusesCookieAttrWithoutWildcard(t *testing.T) {
	_, err, n := cookieImport(t, "ck", "cookie:sid", map[string]bool{"counter": true})
	if err == nil {
		t.Fatal("import succeeded without the wildcard")
	}
	if !errors.Is(err, evidence.ErrEventInvalid) {
		t.Logf("store did not pass ErrEventInvalid through: %v", err)
	}
	if n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
}

func TestImportRefusesBadCookieKeyEvenWithWildcard(t *testing.T) {
	_, err, n := cookieImport(t, "ck", "cookie:UP", map[string]bool{"counter": true, "cookie:*": true})
	if err == nil {
		t.Fatal("import accepted cookie:UP")
	}
	if n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
}
