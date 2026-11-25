package mcpsrv

import (
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/mcpaudit"
)

// BL-351: the person-facing lifecycle (enroll, approve, scope change, revoke) leaves rows in the call ledger, on a real SQLite file.

// ledgerOldestFirst returns the rows of the ledger in the order they were written.
func ledgerOldestFirst(t *testing.T, e *env) []mcpaudit.Event {
	t.Helper()
	rows, err := e.db.RecentEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func kindsOf(rows []mcpaudit.Event) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

func TestLifecycleLeavesLedgerRows(t *testing.T) {
	e := newEnv(t)
	n1 := nonce(t)
	enr, err := e.g.Enroll(ctx, req("led-host", n1))
	if err != nil {
		t.Fatal(err)
	}
	// The same host enrolling again with the same text changes nothing, so it writes nothing (the bridge enrolls at every start).
	if _, err := e.g.Enroll(ctx, req("led-host", n1)); err != nil {
		t.Fatal(err)
	}
	p1, err := e.g.Approve(ctx, "led-host", appr())
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := e.g.Claim(ctx, "led-host", n1)
	if err != nil {
		t.Fatal(err)
	}
	// A refused approval changes nothing and writes nothing.
	if _, err := e.g.Approve(ctx, "led-host", appr()); err == nil {
		t.Fatal("approving an active profile must fail")
	}
	// Reissue, enroll again and approve with a different scope: a scope change.
	if _, err := e.g.Reissue(ctx, "led-host"); err != nil {
		t.Fatal(err)
	}
	n2 := nonce(t)
	if _, err := e.g.Enroll(ctx, req("led-host", n2)); err != nil {
		t.Fatal(err)
	}
	wide := Approval{Tier: TierEvidence, Sources: []string{"src-1"}, Identifiers: true, Code: true, IdleTTL: DefaultIdleTTL}
	p2, err := e.g.Approve(ctx, "led-host", wide)
	if err != nil {
		t.Fatal(err)
	}
	pRevoked, err := e.g.Revoke(ctx, "led-host")
	if err != nil {
		t.Fatal(err)
	}

	rows := ledgerOldestFirst(t, e)
	want := []string{mcpaudit.KindEnroll, mcpaudit.KindApprove, mcpaudit.KindRevoke, mcpaudit.KindEnroll, mcpaudit.KindApprove, mcpaudit.KindScopeChange, mcpaudit.KindRevoke}
	if got := kindsOf(rows); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for _, r := range rows {
		if r.ProfileID != "led-host" || r.Outcome != mcpaudit.OutcomeOK || r.Time.IsZero() {
			t.Errorf("row %+v: want profile led-host, outcome ok, a time", r)
		}
	}
	if rows[0].ProfileRev != enr.Rev || rows[1].ProfileRev != p1.Rev || rows[4].ProfileRev != p2.Rev || rows[5].ProfileRev != p2.Rev || rows[6].ProfileRev != pRevoked.Rev {
		t.Errorf("revisions: %d %d %d %d %d", rows[0].ProfileRev, rows[1].ProfileRev, rows[4].ProfileRev, rows[5].ProfileRev, rows[6].ProfileRev)
	}
	if rows[2].Code != "reissued" || rows[6].Code != "revoked" {
		t.Errorf("revoke codes: %q %q", rows[2].Code, rows[6].Code)
	}
	if !strings.Contains(rows[5].Code, "evidence") || !strings.Contains(rows[5].Code, "sources=1") {
		t.Errorf("scope_change should say the new scope, got %q", rows[5].Code)
	}

	// Nothing secret or identifying: no credential, nonce or digest in any field of any row; no exe path.
	for _, r := range rows {
		blob := fmt.Sprintf("%+v", r)
		for _, bad := range []string{secret, n1, n2, `Program Files`, "SEALED"} {
			if strings.Contains(blob, bad) {
				t.Errorf("row %+v holds %q", r, bad)
			}
		}
		if len(r.Fingerprint) != 0 {
			t.Errorf("a lifecycle row has no argument digest: %+v", r)
		}
	}
}

func TestFirstApprovalIsNotAScopeChange(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	if _, err := e.g.Enroll(ctx, req("first", n)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "first", appr()); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(ledgerOldestFirst(t, e)); strings.Join(got, ",") != "enroll,approve" {
		t.Fatalf("kinds = %v", got)
	}
	// Reissue then approve with the very same scope: no scope_change either.
	if _, err := e.g.Reissue(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Enroll(ctx, req("first", nonce(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "first", appr()); err != nil {
		t.Fatal(err)
	}
	for _, r := range ledgerOldestFirst(t, e) {
		if r.Kind == mcpaudit.KindScopeChange {
			t.Fatalf("unexpected scope_change: %+v", r)
		}
	}
}

func TestAddedHostAndContestedEnrollAreLogged(t *testing.T) {
	e := newEnv(t)
	if err := e.g.CreatePending(ctx, "by-person"); err != nil {
		t.Fatal(err)
	}
	n := nonce(t)
	if _, err := e.g.Enroll(ctx, req("by-person", n)); err != nil {
		t.Fatal(err)
	}
	// A second process with another nonce is contested: it is logged, bounded by the contested counter.
	if _, err := e.g.Enroll(ctx, req("by-person", nonce(t))); err != nil {
		t.Fatal(err)
	}
	rows := ledgerOldestFirst(t, e)
	if got := kindsOf(rows); strings.Join(got, ",") != "enroll,enroll,enroll" {
		t.Fatalf("kinds = %v", got)
	}
	if rows[0].Code != "added" || rows[1].Code != "host_seen" || rows[2].Code != "contested" {
		t.Errorf("codes: %q %q %q", rows[0].Code, rows[1].Code, rows[2].Code)
	}
}

func TestSameScopeWithCodeModeAfterReissueIsNotAScopeChange(t *testing.T) {
	e := newEnv(t)
	withCode := Approval{Tier: TierEvidence, Sources: []string{"src-1"}, Code: true, IdleTTL: DefaultIdleTTL}
	if _, err := e.g.Enroll(ctx, req("coded", nonce(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "coded", withCode); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Reissue(ctx, "coded"); err != nil { // switches code mode off until the next approval says again
		t.Fatal(err)
	}
	if _, err := e.g.Enroll(ctx, req("coded", nonce(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "coded", withCode); err != nil {
		t.Fatal(err)
	}
	for _, r := range ledgerOldestFirst(t, e) {
		if r.Kind == mcpaudit.KindScopeChange {
			t.Fatalf("the person approved the same scope again; unexpected %+v", r)
		}
	}
}

func TestEndingWhatIsNotLiveLeavesNoRevokeRow(t *testing.T) {
	e := newEnv(t)
	if _, err := e.g.Enroll(ctx, req("idle", nonce(t))); err != nil {
		t.Fatal(err)
	}
	// Pending: reissuing or revoking it ends no access.
	if _, err := e.g.Reissue(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Enroll(ctx, req("idle", nonce(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "idle", appr()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Revoke(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	// Revoking again changes no access.
	if _, err := e.g.Revoke(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range ledgerOldestFirst(t, e) {
		if r.Kind == mcpaudit.KindRevoke {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("revoke rows = %d, want 1 (only the live grant ending)", n)
	}
}
