package mcpsrv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/mcpaudit"
)

// S1: a stop whose flag cannot be written still stops, cancels and runs the hooks, and says so.
func TestStopFailsClosedWhenTheFlagCannotBeSaved(t *testing.T) {
	e := newEnv(t)
	h, pr := e.enrolled("h1")
	e.enrolled("idle")
	var hooked []string
	e.g.OnRevoke(func(id string) { hooked = append(hooked, id) })
	callCtx, done, err := e.g.BeginCall(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err := e.db.Close(); err != nil { // every write and read now fails
		t.Fatal(err)
	}
	if err := e.g.Stop(ctx); !errors.Is(err, ErrStopNotSaved) {
		t.Fatalf("Stop: %v", err)
	}
	if !e.g.Stopped() {
		t.Fatal("the stop did not take effect in memory")
	}
	if !errors.Is(context.Cause(callCtx), ErrAccessEnded) {
		t.Fatalf("running call not cancelled: %v", context.Cause(callCtx))
	}
	if len(hooked) == 0 || hooked[0] != "h1" {
		t.Fatalf("hooks: %v", hooked)
	}
	// Even with the store gone, a call is not let through by the in-memory flag alone: the profile read fails, never succeeds.
	if _, err := e.g.Authenticate(ctx, h); err == nil {
		t.Fatal("a call was authenticated with the store closed")
	}
	// Resume that cannot be saved leaves the stop in place.
	if err := e.g.Resume(ctx); err == nil || !e.g.Stopped() {
		t.Fatalf("resume with an unwritable store: %v stopped=%v", err, e.g.Stopped())
	}
}

// Stop runs the hooks of profiles that have no call running too.
func TestStopRunsHooksForIdleProfiles(t *testing.T) {
	e := newEnv(t)
	e.enrolled("a1")
	e.enrolled("a2")
	_, _ = e.g.Enroll(ctx, req("p1", nonce(t)))
	var hooked []string
	e.g.OnRevoke(func(id string) { hooked = append(hooked, id) })
	if err := e.g.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if len(hooked) != 3 {
		t.Fatalf("hooks %v", hooked)
	}
}

// S2: text shown on the approval card cannot carry bidi overrides, zero-width or other invisible characters.
func TestHostTextRefusesInvisibleCharacters(t *testing.T) {
	bad := map[string]string{
		"rlo":             "evil\u202eexe.dll",
		"lre":             "a\u202ab",
		"isolate":         "a\u2066b\u2069",
		"zero width":      "a\u200bb",
		"zwj":             "a\u200db",
		"lrm":             "a\u200eb",
		"bom":             "\ufeffa",
		"word joiner":     "a\u2060b",
		"soft hyphen":     "a\u00adb",
		"private use":     "a\ue000b",
		"unassigned":      "a\u0378b",
		"tag char":        "a\U000e0041b",
		"line separator":  "a\u2028b",
		"next line":       "a\u0085b",
		"tab":             "a\tb",
		"del":             "a\x7fb",
		"replacement bad": "a\xffb",
	}
	for name, s := range bad {
		if validText(s, 200) {
			t.Errorf("%s accepted in validText", name)
		}
		r := req("h1", nonce(t))
		r.HostName = s
		e := newEnv(t)
		if _, err := e.g.Enroll(ctx, r); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: host name accepted: %v", name, err)
		}
		r = req("h1", nonce(t))
		r.Exe = `C:\x\` + s
		if _, err := e.g.Enroll(ctx, r); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: exe accepted: %v", name, err)
		}
		if n, _ := e.db.Count(ctx); n != 0 {
			t.Errorf("%s left a row", name)
		}
	}
	for _, s := range []string{"Claude Code", `C:\Program Files\Unravis\unravis.exe`, `C:\Users\Zoë\App Data\ünïcode.exe`, "日本語ホスト", "2.1.330-beta+1", "a\u00a0b"} {
		if !validText(s, 200) {
			t.Errorf("%q refused", s)
		}
	}
	// The same rule for source ids in an approval.
	e := newEnv(t)
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	a := appr()
	a.Sources = []string{"src\u202e1"}
	if _, err := e.g.Approve(ctx, "h1", a); !errors.Is(err, ErrBadApproval) {
		t.Fatalf("bidi source id: %v", err)
	}
}

// S3: a profile that is removed and made again never repeats a (profile, revision) pair of the old one.
func TestRevisionNeverRepeatsAfterRemove(t *testing.T) {
	e := newEnv(t)
	_, old := e.enrolled("h1") // rev 2
	if _, err := e.g.Revoke(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := e.g.Remove(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	e.restart() // the floor is stored, not in memory
	n := nonce(t)
	r, err := e.g.Enroll(ctx, req("h1", n))
	if err != nil || r.Rev <= old.Rev+1 {
		t.Fatalf("new profile starts at %d (old revisions went to %d): %v", r.Rev, old.Rev+1, err)
	}
	p, err := e.g.Approve(ctx, "h1", appr())
	if err != nil || p.Rev <= old.Rev+1 {
		t.Fatalf("approved at %d: %v", p.Rev, err)
	}
	secret, _, _ := e.g.Claim(ctx, "h1", n)
	pr, err := e.g.Authenticate(ctx, credScheme+"h1."+secret)
	if err != nil || pr == old {
		t.Fatalf("principal %+v equals the old one %+v (%v)", pr, old, err)
	}
	wantDenied(t, e.g.Recheck(ctx, old), ReasonRevision)
	// A profile of another id made later also starts above the floor.
	if r2, _ := e.g.Enroll(ctx, req("h2", nonce(t))); r2.Rev <= old.Rev+1 {
		t.Fatalf("other id starts at %d", r2.Rev)
	}
	// CreatePending too.
	if err := e.g.CreatePending(ctx, "h3"); err != nil {
		t.Fatal(err)
	}
	if p3, _ := e.db.Get(ctx, "h3"); p3.Rev <= old.Rev+1 {
		t.Fatalf("CreatePending starts at %d", p3.Rev)
	}
}

// S4: a second nonce is recorded, counted once per attempt, capped, never moves the claim, and Reissue clears it.
func TestContestedEnrollIsRecorded(t *testing.T) {
	e := newEnv(t)
	first := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", first))
	if p, _ := e.db.Get(ctx, "h1"); p.Contested != 0 {
		t.Fatalf("contested %d on the first enroll", p.Contested)
	}
	// The same nonce again (the bridge enrolls twice) is not a contest.
	_, _ = e.g.Enroll(ctx, req("h1", first))
	if p, _ := e.db.Get(ctx, "h1"); p.Contested != 0 {
		t.Fatalf("contested %d after a repeat of the same nonce", p.Contested)
	}
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	p, _ := e.db.Get(ctx, "h1")
	if p.Contested != 2 || p.State != "pending" {
		t.Fatalf("after two other nonces: %+v", p)
	}
	// It survives a restart and is bounded.
	e.restart()
	for i := 0; i < maxContested+20; i++ {
		_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	}
	if p, _ := e.db.Get(ctx, "h1"); p.Contested != maxContested {
		t.Fatalf("contested %d, want the cap %d", p.Contested, maxContested)
	}
	// The same holds while the credential waits to be claimed.
	ac := appr()
	ac.AcceptContested = true
	if _, err := e.g.Approve(ctx, "h1", ac); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Reissue(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.db.Get(ctx, "h1"); p.Contested != 0 {
		t.Fatalf("reissue kept the contest: %d", p.Contested)
	}
	e2 := newEnv(t)
	n := nonce(t)
	_, _ = e2.g.Enroll(ctx, req("h1", n))
	if _, err := e2.g.Approve(ctx, "h1", appr()); err != nil {
		t.Fatal(err)
	}
	_, _ = e2.g.Enroll(ctx, req("h1", nonce(t))) // active, not yet claimed
	if p, _ := e2.db.Get(ctx, "h1"); p.Contested != 1 {
		t.Fatalf("contest while unclaimed: %d", p.Contested)
	}
	if _, _, err := e2.g.Claim(ctx, "h1", n); err != nil { // the real bridge still claims
		t.Fatal(err)
	}
	_, _ = e2.g.Enroll(ctx, req("h1", nonce(t))) // claimed: a later enroll is not part of the claim race
	if p, _ := e2.db.Get(ctx, "h1"); p.Contested != 1 {
		t.Fatalf("contest counted after the claim: %d", p.Contested)
	}
}

// N5: an approval is bound to the revision the person saw.
func TestStaleApprovalRefused(t *testing.T) {
	e := newEnv(t)
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	p, _ := e.db.Get(ctx, "h1")
	a := appr()
	a.ExpectRev = p.Rev + 1
	if _, err := e.g.Approve(ctx, "h1", a); !errors.Is(err, ErrStaleApproval) {
		t.Fatalf("%v", err)
	}
	a.ExpectRev = p.Rev
	if _, err := e.g.Approve(ctx, "h1", a); err != nil {
		t.Fatal(err)
	}
}

// N11: a claim digest is never a grant digest (the MAC domain), even for the same bytes.
func TestClaimDigestIsNotAGrantDigest(t *testing.T) {
	e := newEnv(t)
	e.enrolled("h1")
	same := strings.Repeat("Q", secretLen) // 43 chars: shaped like a secret
	if _, err := e.db.Update(ctx, "h1", func(p *Profile) error { p.SecretDigest = e.db.MAC(macClaim, []byte(same)); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Authenticate(ctx, credScheme+"h1."+same); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a claim-domain digest authenticated a call: %v", err)
	}
}

// N3: steady use at the minimum idle time never expires, even with calls just under and just over the touch interval.
func TestSteadyUseNeverExpiresAtTheMinimum(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", n))
	a := appr()
	a.IdleTTL = MinIdleTTL
	if _, err := e.g.Approve(ctx, "h1", a); err != nil {
		t.Fatal(err)
	}
	secret, _, _ := e.g.Claim(ctx, "h1", n)
	h := credScheme + "h1." + secret
	for i, gap := range []time.Duration{0, 59 * time.Second, 2 * time.Second, 59 * time.Second, 61 * time.Second, 59 * time.Second, 61 * time.Second} {
		e.clk.Add(gap)
		if _, err := e.g.Authenticate(ctx, h); err != nil {
			t.Fatalf("call %d after %v: %v", i, gap, err)
		}
	}
	for i := 0; i < 40; i++ {
		e.clk.Add(59 * time.Second)
		if _, err := e.g.Authenticate(ctx, h); err != nil {
			t.Fatalf("steady call %d: %v", i, err)
		}
	}
}

// S5 and N8 at the grant level: the folder and the files are the current user's only.
func TestStoreFilesAndFolderAreOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mcp")
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // the test makes a loose folder on purpose
		t.Fatal(err)
	}
	db, _, err := mcpaudit.Open(ctx, dir, xorProt{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	g, _ := NewGrants(ctx, db, nil)
	_, _ = g.Enroll(ctx, req("h1", nonce(t))) // a write, so the -wal exists
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("folder %v %v", st, err)
	}
	seen := 0
	for _, name := range []string{mcpaudit.FileName, mcpaudit.FileName + "-wal", mcpaudit.FileName + "-shm"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		seen++
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode %v", name, st.Mode())
		}
	}
	if seen < 2 {
		t.Fatalf("only %d store files seen", seen)
	}
}
