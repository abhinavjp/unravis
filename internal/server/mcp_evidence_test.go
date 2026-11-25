package server

import (
	"context"
	"net/http"
	"testing"
)

// BL-349 (review of PR 269, S1): the evidence resolver is closed while the master key is locked, and the lock runs the owner's hook, so
// the MCP hosts' handles and aliases end with it.
func TestEvidenceRouteIsLockedWithTheMasterKey(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	const alias = "/api/mcp/evidence/ev1_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	SetEvidenceResolver(e.h, func(context.Context, string) (EvidenceTarget, bool) {
		return EvidenceTarget{SourceID: "s", RecordID: "r"}, true
	})
	locks := 0
	SetMasterKeyLockHook(e.h, func() { locks++ })

	// locked from the start (the key file exists, nothing unlocked it): 423, the resolver is not asked
	if res := e.do(http.MethodGet, alias, ""); res.Code != http.StatusLocked {
		t.Fatalf("locked at start: %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock: %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodGet, alias, ""); res.Code != http.StatusOK {
		t.Fatalf("unlocked: %d %s", res.Code, res.Body)
	}
	if locks != 0 {
		t.Fatalf("the lock hook ran %d times before a lock", locks)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", res.Code, res.Body)
	}
	if locks != 1 {
		t.Errorf("the lock hook ran %d times", locks)
	}
	if res := e.do(http.MethodGet, alias, ""); res.Code != http.StatusLocked {
		t.Errorf("after lock: %d %s", res.Code, res.Body)
	}
}

// With no master key set at all, the route is not gated (nothing to lock).
func TestEvidenceRouteWithNoMasterKeyIsNotGated(t *testing.T) {
	e := newMKEnv(t)
	SetEvidenceResolver(e.h, func(context.Context, string) (EvidenceTarget, bool) { return EvidenceTarget{}, false })
	if res := e.do(http.MethodGet, "/api/mcp/evidence/ev1_aaaaaaaaaaaaaaaaaaaaaaaaaa", ""); res.Code != http.StatusNotFound {
		t.Errorf("no key: %d %s", res.Code, res.Body)
	}
}
