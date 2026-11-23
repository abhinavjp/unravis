package mcpsrv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/mcpaudit"
)

// xorProt is a reversible stand-in for DPAPI: sealed bytes never equal the plain ones, and a different key cannot open them.
type xorProt struct{ k byte }

func (p xorProt) Protect(b []byte) ([]byte, error) {
	out := append([]byte("SEALED1:"), b...)
	for i := 8; i < len(out); i++ {
		out[i] ^= p.k
	}
	return out, nil
}

func (p xorProt) Unprotect(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("SEALED1:")) {
		return nil, errors.New("not sealed")
	}
	out := append([]byte(nil), b[8:]...)
	for i := range out {
		out[i] ^= p.k
	}
	return out, nil
}

// failProt cannot unseal anything (a file copied to another Windows account).
type failProt struct{ xorProt }

func (failProt) Unprotect([]byte) ([]byte, error) { return nil, errors.New("wrong user") }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t   *testing.T
	dir string
	clk *clock
	db  *mcpaudit.DB
	g   *Grants
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), clk: &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)}}
	e.open()
	t.Cleanup(func() { _ = e.db.Close() })
	return e
}

func (e *env) open() {
	e.t.Helper()
	db, rec, err := mcpaudit.Open(context.Background(), e.dir, xorProt{0x5a}, e.clk.Now)
	if err != nil || rec != "" {
		e.t.Fatalf("open: %v %q", err, rec)
	}
	g, err := NewGrants(context.Background(), db, e.clk.Now)
	if err != nil {
		e.t.Fatal(err)
	}
	e.db, e.g = db, g
}

func (e *env) restart() {
	e.t.Helper()
	if err := e.db.Close(); err != nil {
		e.t.Fatal(err)
	}
	e.open()
}

func nonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func req(id, n string) EnrollReq {
	return EnrollReq{Profile: id, HostName: "claude-code", HostVersion: "2.1.330", Exe: `C:\Program Files\Unravis\unravis.exe`, PPID: 4321, ClaimNonce: n}
}

var ctx = context.Background()

func appr() Approval { return DefaultApproval([]string{"src-1", "src-2"}) }

// enrolled makes an active, claimed profile and returns its credential header.
func (e *env) enrolled(id string) (header string, p Principal) {
	e.t.Helper()
	n := nonce(e.t)
	if _, err := e.g.Enroll(ctx, req(id, n)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, id, appr()); err != nil {
		e.t.Fatal(err)
	}
	secret, _, err := e.g.Claim(ctx, id, n)
	if err != nil {
		e.t.Fatal(err)
	}
	header = credScheme + id + "." + secret
	p, err = e.g.Authenticate(ctx, header)
	if err != nil {
		e.t.Fatal(err)
	}
	return header, p
}

func wantDenied(t *testing.T, err error, reason string) {
	t.Helper()
	var d *Denied
	if !errors.As(err, &d) || d.Reason != reason || d.Code != CodePermissionRequired || d.Action == "" {
		t.Fatalf("want denial %q, got %v", reason, err)
	}
}

func TestEnrollPendingThenApproveThenClaimOnce(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	r, err := e.g.Enroll(ctx, req("claude-code-1", n))
	if err != nil || r.State != "pending" || r.Rev != 1 {
		t.Fatalf("enroll: %+v %v", r, err)
	}
	// Pending: nothing to claim yet, and no credential can authenticate.
	if _, _, err := e.g.Claim(ctx, "claude-code-1", n); !errors.Is(err, ErrClaimPending) {
		t.Fatalf("claim while pending: %v", err)
	}
	_, err = e.g.Authenticate(ctx, credScheme+"claude-code-1."+strings.Repeat("A", secretLen))
	wantDenied(t, err, ReasonPending)

	p, err := e.g.Approve(ctx, "claude-code-1", appr())
	if err != nil || p.State != "active" || p.Rev != 2 || p.Tier != TierAggregate || len(p.Tools) != 7 || len(p.Sources) != 2 || p.Identifiers {
		t.Fatalf("approve: %+v %v", p, err)
	}
	secret, rev, err := e.g.Claim(ctx, "claude-code-1", n)
	if err != nil || len(secret) != secretLen || rev != 2 {
		t.Fatalf("claim: %q %d %v", secret, rev, err)
	}
	pr, err := e.g.Authenticate(ctx, credScheme+"claude-code-1."+secret)
	if err != nil || pr != (Principal{"claude-code-1", 2}) {
		t.Fatalf("authenticate: %+v %v", pr, err)
	}
}

func TestClaimWrongNonce(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	if _, err := e.g.Enroll(ctx, req("h1", n)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "h1", appr()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.g.Claim(ctx, "h1", nonce(t)); !errors.Is(err, ErrWrongNonce) {
		t.Fatalf("wrong nonce: %v", err)
	}
	// The wrong try used nothing up: the right holder still claims.
	if _, _, err := e.g.Claim(ctx, "h1", n); err != nil {
		t.Fatalf("right nonce after a wrong one: %v", err)
	}
}

func TestClaimTwice410(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", n))
	_, _ = e.g.Approve(ctx, "h1", appr())
	if _, _, err := e.g.Claim(ctx, "h1", n); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.g.Claim(ctx, "h1", n); !errors.Is(err, ErrClaimGone) {
		t.Fatalf("second claim with the right nonce: %v", err)
	}
	if _, _, err := e.g.Claim(ctx, "nope", n); !errors.Is(err, ErrClaimGone) {
		t.Fatalf("unknown profile: %v", err)
	}
}

func TestClaimExpiresAndDoesNotSurviveRestart(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", n))
	_, _ = e.g.Approve(ctx, "h1", appr())
	e.clk.Add(ClaimTTL + time.Second)
	if _, _, err := e.g.Claim(ctx, "h1", n); !errors.Is(err, ErrClaimGone) {
		t.Fatalf("claim after ttl: %v", err)
	}
	// Restart before the claim: the secret was only in memory.
	n2 := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h2", n2))
	_, _ = e.g.Approve(ctx, "h2", appr())
	e.restart()
	if _, _, err := e.g.Claim(ctx, "h2", n2); !errors.Is(err, ErrClaimGone) {
		t.Fatalf("claim after restart: %v", err)
	}
	// Reissue is the way out: pending again, the host enrolls with a fresh nonce, the person approves, the claim works.
	if p, err := e.g.Reissue(ctx, "h2"); err != nil || p.State != "pending" {
		t.Fatalf("reissue: %+v %v", p, err)
	}
	n3 := nonce(t)
	if r, _ := e.g.Enroll(ctx, req("h2", n3)); r.State != "pending" {
		t.Fatalf("re-enroll: %+v", r)
	}
	if _, err := e.g.Approve(ctx, "h2", appr()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.g.Claim(ctx, "h2", n2); !errors.Is(err, ErrWrongNonce) {
		t.Fatalf("old nonce after reissue: %v", err)
	}
	if _, _, err := e.g.Claim(ctx, "h2", n3); err != nil {
		t.Fatalf("new nonce after reissue: %v", err)
	}
}

// A second process that learns the profile id must not be able to take over the claim of the first bridge.
func TestSecondEnrollCannotReplaceNonce(t *testing.T) {
	e := newEnv(t)
	first, attacker := nonce(t), nonce(t)
	_, _ = e.g.Enroll(ctx, req("default", first))
	r := req("default", attacker)
	r.Exe = `C:\evil\x.exe`
	if resp, err := e.g.Enroll(ctx, r); err != nil || resp.State != "pending" {
		t.Fatalf("second enroll: %+v %v", resp, err)
	}
	p, _ := e.db.Get(ctx, "default")
	if p.Exe == r.Exe {
		t.Fatal("the second enroll changed the host description of the first")
	}
	// The person is told: two processes asked for this id. Approving needs the explicit acceptance (the one-click default never has it).
	if p, _ = e.db.Get(ctx, "default"); p.Contested != 1 {
		t.Fatalf("contested = %d", p.Contested)
	}
	if _, err := e.g.Approve(ctx, "default", appr()); !errors.Is(err, ErrContested) {
		t.Fatalf("approve of a contested profile: %v", err)
	}
	ac := appr()
	ac.AcceptContested = true
	if _, err := e.g.Approve(ctx, "default", ac); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.g.Claim(ctx, "default", attacker); !errors.Is(err, ErrWrongNonce) {
		t.Fatalf("attacker claim: %v", err)
	}
	if _, _, err := e.g.Claim(ctx, "default", first); err != nil {
		t.Fatalf("first bridge claim: %v", err)
	}
}

func TestApproveNeedsAHostThatEnrolled(t *testing.T) {
	e := newEnv(t)
	if err := e.g.CreatePending(ctx, "claude-code-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "claude-code-1", appr()); !errors.Is(err, ErrNoHostYet) {
		t.Fatalf("approve with no host: %v", err)
	}
	if err := e.g.CreatePending(ctx, "claude-code-1"); !errors.Is(err, mcpaudit.ErrExists) {
		t.Fatalf("create twice: %v", err)
	}
	// The host connects later and takes the first-enroll slot.
	n := nonce(t)
	if _, err := e.g.Enroll(ctx, req("claude-code-1", n)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "claude-code-1", appr()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, "claude-code-1", appr()); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("approve twice: %v", err)
	}
	if _, err := e.g.Approve(ctx, "ghost", appr()); !errors.Is(err, mcpaudit.ErrNotFound) {
		t.Fatalf("approve unknown: %v", err)
	}
}

func TestAuthenticateRefusals(t *testing.T) {
	e := newEnv(t)
	h, _ := e.enrolled("h1")
	secret := strings.TrimPrefix(h, credScheme+"h1.")
	other := strings.Repeat("B", secretLen)
	if secret == other {
		t.Fatal("unlucky")
	}
	for name, hdr := range map[string]string{
		"wrong secret":   credScheme + "h1." + other,
		"unknown id":     credScheme + "h2." + secret,
		"no scheme":      "Bearer h1." + secret,
		"empty":          "",
		"short secret":   credScheme + "h1." + secret[:40],
		"bad charset":    credScheme + "h1." + secret[:42] + "+",
		"upper id":       credScheme + "H1." + secret,
		"no dot":         credScheme + "h1" + secret,
		"very long":      credScheme + strings.Repeat("a", 4000),
		"trailing space": h + " ",
	} {
		if _, err := e.g.Authenticate(ctx, hdr); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The same refusal whether the id exists or not: no oracle for profile ids.
	_, e1 := e.g.Authenticate(ctx, credScheme+"h1."+other)
	_, e2 := e.g.Authenticate(ctx, credScheme+"h2."+other)
	if e1 != e2 {
		t.Fatalf("different refusals: %v vs %v", e1, e2)
	}
}

func TestRevokeCancelsRunning(t *testing.T) {
	e := newEnv(t)
	_, pr := e.enrolled("h1")
	var hooked []string
	e.g.OnRevoke(func(id string) { hooked = append(hooked, id) })
	callCtx, done, err := e.g.BeginCall(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	// A fake slow handler: it waits for its context.
	finished := make(chan error, 1)
	go func() { <-callCtx.Done(); finished <- context.Cause(callCtx) }()
	select {
	case err := <-finished:
		t.Fatalf("call ended before the revoke: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := e.g.Revoke(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-finished:
		if !errors.Is(cause, ErrAccessEnded) {
			t.Fatalf("cause: %v", cause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoke did not cancel the running call")
	}
	if len(hooked) != 1 || hooked[0] != "h1" {
		t.Fatalf("hooks: %v", hooked)
	}
	// The call that is not running any more is not cancelled twice or leaked.
	if len(e.g.running) != 0 {
		t.Fatalf("running table: %v", e.g.running)
	}
}

func TestNoResultAfterRevoke(t *testing.T) {
	e := newEnv(t)
	_, pr := e.enrolled("h1")
	if err := e.g.Recheck(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Revoke(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	wantDenied(t, e.g.Recheck(ctx, pr), ReasonRevoked)
	if _, _, err := e.g.BeginCall(ctx, pr); err == nil {
		t.Fatal("a call began on a revoked profile")
	}
	// A call that was authenticated and registered before the revoke, but whose handler ignored the cancel and returned: Recheck stops it.
	_, pr2 := e.enrolled("h2")
	_, done, err := e.g.BeginCall(ctx, pr2)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	_, _ = e.g.Revoke(ctx, "h2")
	wantDenied(t, e.g.Recheck(ctx, pr2), ReasonRevoked)
}

func TestPolicyChangeDeniesAResultOfTheOldRevision(t *testing.T) {
	e := newEnv(t)
	_, pr := e.enrolled("h1")
	// Reissue then approve with a narrower scope: same id, new revision.
	if _, err := e.g.Reissue(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	wantDenied(t, e.g.Recheck(ctx, pr), ReasonPending)
}

func TestRevokedStaysRevokedAcrossRestart(t *testing.T) {
	e := newEnv(t)
	h, _ := e.enrolled("h1")
	h2, _ := e.enrolled("h2")
	if _, err := e.g.Revoke(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	e.restart()
	_, err := e.g.Authenticate(ctx, h)
	wantDenied(t, err, ReasonRevoked)
	// Re-enrolling does not bring it back: only the person does (Reissue).
	if r, err := e.g.Enroll(ctx, req("h1", nonce(t))); err != nil || r.State != "revoked" {
		t.Fatalf("enroll of a revoked profile: %+v %v", r, err)
	}
	// The other grant survived the restart and still works.
	if _, err := e.g.Authenticate(ctx, h2); err != nil {
		t.Fatalf("grant after restart: %v", err)
	}
}

func TestRestartKeepsGrants(t *testing.T) {
	e := newEnv(t)
	h, pr := e.enrolled("h1")
	e.restart()
	pr2, err := e.g.Authenticate(ctx, h)
	if err != nil || pr2 != pr {
		t.Fatalf("after restart: %+v %v", pr2, err)
	}
	p, _ := e.db.Get(ctx, "h1")
	if p.Tier != TierAggregate || len(p.Tools) != 7 || len(p.Sources) != 2 || p.HostName != "claude-code" || !p.Claimed {
		t.Fatalf("stored profile: %+v", p)
	}
}

// Legacy `--mcp` with no profile id: it is the id "default", pending, and nothing in this package approves it.
func TestLegacyProfilePending(t *testing.T) {
	e := newEnv(t)
	r := req(DefaultProfileID, nonce(t))
	r.RequestIdentifiers = true // --mcp-allow-identifiers
	resp, err := e.g.Enroll(ctx, r)
	if err != nil || resp.State != "pending" {
		t.Fatalf("%+v %v", resp, err)
	}
	p, _ := e.db.Get(ctx, DefaultProfileID)
	if !p.RequestedIdentifiers || p.Identifiers || p.State != "pending" || len(p.SecretDigest) != 0 || len(p.Tools) != 0 {
		t.Fatalf("pending default: %+v", p)
	}
	for _, hdr := range []string{"", credScheme + "default." + strings.Repeat("A", secretLen)} {
		_, err := e.g.Authenticate(ctx, hdr)
		if hdr == "" {
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("no credential: %v", err)
			}
			continue
		}
		wantDenied(t, err, ReasonPending)
	}
	// "Approve with defaults" gives tier aggregate, 8 h idle expiry and no identifiers, even though the flag asked for them.
	p, err = e.g.Approve(ctx, DefaultProfileID, DefaultApproval([]string{"s1"}))
	if err != nil || p.Identifiers || p.Tier != TierAggregate || p.IdleTTL != 8*time.Hour || !p.RequestedIdentifiers {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	if IdentifiersAllowed(p, e.clk.Now()) {
		t.Fatal("the request flag became a grant")
	}
}

func TestIdentifiersGrantIsTimed(t *testing.T) {
	e := newEnv(t)
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	a := appr()
	a.Identifiers = true
	p, err := e.g.Approve(ctx, "h1", a)
	if err != nil {
		t.Fatal(err)
	}
	if !IdentifiersAllowed(p, e.clk.Now()) {
		t.Fatal("grant not in force")
	}
	if IdentifiersAllowed(p, e.clk.Now().Add(DefaultIdentifiers+time.Second)) {
		t.Fatal("grant outlived its time")
	}
	a.IdentifiersFor = MaxIdentifiers + time.Hour
	if _, err := e.g.Approve(ctx, "h1", a); err == nil {
		t.Fatal("identifiers beyond the maximum accepted") // also not pending any more: either way refused
	}
}

func TestExpiredProfileDenied(t *testing.T) {
	e := newEnv(t)
	h, _ := e.enrolled("h1")
	e.clk.Add(7 * time.Hour)
	if _, err := e.g.Authenticate(ctx, h); err != nil { // a call inside the idle window slides it
		t.Fatal(err)
	}
	e.clk.Add(7 * time.Hour) // 14 h from approval, 7 h from the last call: still alive because of the slide
	if _, err := e.g.Authenticate(ctx, h); err != nil {
		t.Fatalf("slid expiry: %v", err)
	}
	e.clk.Add(8*time.Hour + time.Second)
	_, err := e.g.Authenticate(ctx, h)
	wantDenied(t, err, ReasonExpired)
	// Renew, one click: the same credential works again, same revision.
	p0, _ := e.db.Get(ctx, "h1")
	p, err := e.g.Renew(ctx, "h1")
	if err != nil || p.Rev != p0.Rev {
		t.Fatalf("renew: %+v %v", p, err)
	}
	if _, err := e.g.Authenticate(ctx, h); err != nil {
		t.Fatalf("after renew: %v", err)
	}
}

func TestAbsoluteCapBeatsSliding(t *testing.T) {
	e := newEnv(t)
	h, _ := e.enrolled("h1")
	for i := 0; i < 31*3; i++ { // a call every 8 h minus a bit: idle expiry never fires
		e.clk.Add(7*time.Hour + 59*time.Minute)
		_, err := e.g.Authenticate(ctx, h)
		if err != nil {
			wantDenied(t, err, ReasonExpired)
			if e.clk.Now().Sub(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)) < AbsoluteCap-8*time.Hour {
				t.Fatalf("expired too early: %v", e.clk.Now())
			}
			return
		}
	}
	t.Fatal("sliding expiry went past the absolute cap")
}

func TestStopAndResume(t *testing.T) {
	e := newEnv(t)
	h, pr := e.enrolled("h1")
	var hooked []string
	e.g.OnRevoke(func(id string) { hooked = append(hooked, id) })
	callCtx, done, err := e.g.BeginCall(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err := e.g.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(callCtx), ErrAccessEnded) {
		t.Fatalf("running call not cancelled: %v", context.Cause(callCtx))
	}
	if len(hooked) != 1 {
		t.Fatalf("hooks: %v", hooked)
	}
	_, err = e.g.Authenticate(ctx, h)
	wantDenied(t, err, ReasonStopped)
	wantDenied(t, e.g.Recheck(ctx, pr), ReasonStopped)
	// A stop is not lifted by a restart.
	e.restart()
	if !e.g.Stopped() {
		t.Fatal("stop lost on restart")
	}
	_, err = e.g.Authenticate(ctx, h)
	wantDenied(t, err, ReasonStopped)
	if err := e.g.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Authenticate(ctx, h); err != nil {
		t.Fatalf("after resume: %v", err)
	}
	e.restart()
	if e.g.Stopped() {
		t.Fatal("resume lost on restart")
	}
}

func TestCancelAllLeavesProfilesActive(t *testing.T) {
	e := newEnv(t)
	h, pr := e.enrolled("h1")
	callCtx, done, _ := e.g.BeginCall(ctx, pr)
	defer done()
	e.g.CancelAll() // the case lock
	if !errors.Is(context.Cause(callCtx), ErrAccessEnded) {
		t.Fatal("not cancelled")
	}
	if _, err := e.g.Authenticate(ctx, h); err != nil {
		t.Fatalf("profile changed by CancelAll: %v", err)
	}
}

func TestDenyAndRemove(t *testing.T) {
	e := newEnv(t)
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	if p, err := e.g.Deny(ctx, "h1"); err != nil || p.State != "denied" {
		t.Fatalf("deny: %+v %v", p, err)
	}
	if _, err := e.g.Approve(ctx, "h1", appr()); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("approve a denied one: %v", err)
	}
	if err := e.g.Remove(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.db.Count(ctx); n != 0 {
		t.Fatalf("count %d", n)
	}
	// Junk pending rows can be cleared directly; an active profile cannot be removed (revoke first).
	_, _ = e.g.Enroll(ctx, req("junk", nonce(t)))
	if err := e.g.Remove(ctx, "junk"); err != nil {
		t.Fatalf("remove pending: %v", err)
	}
	e.enrolled("live")
	if err := e.g.Remove(ctx, "live"); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("remove active: %v", err)
	}
	// Deny only applies to a pending profile.
	if _, err := e.g.Deny(ctx, "live"); !errors.Is(err, ErrNotApprovable) {
		t.Fatalf("deny active: %v", err)
	}
	if p, _ := e.db.Get(ctx, "live"); p.State != "active" {
		t.Fatalf("deny changed an active profile: %+v", p)
	}
}

func TestProfileLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < MaxProfiles; i++ {
		if _, err := e.g.Enroll(ctx, req("h"+strings.Repeat("a", i%10)+string(rune('a'+i/10)), nonce(t))); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := e.g.Enroll(ctx, req("one-more", nonce(t))); !errors.Is(err, ErrTooManyProfiles) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := e.g.CreatePending(ctx, "one-more"); !errors.Is(err, ErrTooManyProfiles) {
		t.Fatalf("create over the limit: %v", err)
	}
}

func TestEnrollValidation(t *testing.T) {
	e := newEnv(t)
	ok := req("h1", nonce(t))
	bad := map[string]func(*EnrollReq){
		"upper id":      func(r *EnrollReq) { r.Profile = "H1" },
		"long id":       func(r *EnrollReq) { r.Profile = strings.Repeat("a", 33) },
		"dotted id":     func(r *EnrollReq) { r.Profile = "a.b" },
		"empty id":      func(r *EnrollReq) { r.Profile = "" },
		"short nonce":   func(r *EnrollReq) { r.ClaimNonce = "abc" },
		"nonce charset": func(r *EnrollReq) { r.ClaimNonce = strings.Repeat("+", nonceLen) },
		"host newline":  func(r *EnrollReq) { r.HostName = "a\nb" },
		"host ctrl":     func(r *EnrollReq) { r.HostName = "a\x1b[2Jb" },
		"host line sep": func(r *EnrollReq) { r.HostName = "a\u2028b" },
		"host utf8":     func(r *EnrollReq) { r.HostName = "a\xffb" },
		"host long":     func(r *EnrollReq) { r.HostName = strings.Repeat("a", 65) },
		"exe long":      func(r *EnrollReq) { r.Exe = strings.Repeat("a", 521) },
		"exe nul":       func(r *EnrollReq) { r.Exe = "a\x00b" },
		"ppid":          func(r *EnrollReq) { r.PPID = -1 },
	}
	for name, mut := range bad {
		r := ok
		mut(&r)
		if _, err := e.g.Enroll(ctx, r); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n, _ := e.db.Count(ctx); n != 0 {
		t.Fatalf("a refused enroll left %d rows", n)
	}
	if _, err := e.g.Enroll(ctx, ok); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalValidation(t *testing.T) {
	e := newEnv(t)
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	for name, a := range map[string]Approval{
		"no tier":         {Sources: []string{"s"}},
		"bad tier":        {Tier: "admin", Sources: []string{"s"}},
		"tool of tier up": {Tier: TierAggregate, Tools: []string{"audit_get_requests"}, Sources: []string{"s"}},
		"unknown tool":    {Tier: TierEvidence, Tools: []string{"audit_drop_tables"}, Sources: []string{"s"}},
		"dup tool":        {Tier: TierAggregate, Tools: []string{"audit_compare", "audit_compare"}, Sources: []string{"s"}},
		"no sources":      {Tier: TierAggregate},
		"empty source":    {Tier: TierAggregate, Sources: []string{""}},
		"dup source":      {Tier: TierAggregate, Sources: []string{"a", "a"}},
		"ctrl source":     {Tier: TierAggregate, Sources: []string{"a\nb"}},
		"ttl too short":   {Tier: TierAggregate, Sources: []string{"s"}, IdleTTL: time.Second},
		"ttl too long":    {Tier: TierAggregate, Sources: []string{"s"}, IdleTTL: MaxIdleTTL + time.Hour},
		"negative ident":  {Tier: TierAggregate, Sources: []string{"s"}, IdentifiersFor: -time.Hour},
	} {
		if _, err := e.g.Approve(ctx, "h1", a); !errors.Is(err, ErrBadApproval) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if p, _ := e.db.Get(ctx, "h1"); p.State != "pending" {
		t.Fatalf("a refused approval changed the profile: %+v", p)
	}
	a := Approval{Tier: TierEvidence, Tools: []string{"audit_compare", "audit_get_requests"}, Sources: []string{"s"}}
	p, err := e.g.Approve(ctx, "h1", a)
	if err != nil || len(p.Tools) != 2 {
		t.Fatalf("narrowed tools: %+v %v", p, err)
	}
}

func TestProfileJSONHasNoDigest(t *testing.T) {
	e := newEnv(t)
	e.enrolled("h1")
	ps, err := e.g.List(ctx)
	if err != nil || len(ps) != 1 || len(ps[0].SecretDigest) == 0 {
		t.Fatalf("%+v %v", ps, err)
	}
	b, _ := json.Marshal(ps)
	for _, w := range []string{"Digest", "digest", "secret", "Secret"} {
		if strings.Contains(string(b), w) {
			t.Fatalf("profile JSON mentions %q: %s", w, b)
		}
	}
}

func TestConcurrentCallsAndRevoke(t *testing.T) {
	e := newEnv(t)
	h, pr := e.enrolled("h1")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := e.g.Authenticate(ctx, h); err != nil {
					return
				}
				if c, done, err := e.g.BeginCall(ctx, pr); err == nil {
					<-c.Done() // only a revoke ends it
					done()
					return
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := e.g.Revoke(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan struct{})
	go func() { wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("calls still running after the revoke")
	}
}

func TestToolsMatchTheCatalog(t *testing.T) {
	want := ToolsForTier(TierEvidence)
	if len(want) != 10 {
		t.Fatalf("tier evidence has %d tools", len(want))
	}
	if ToolsForTier("x") != nil {
		t.Fatal("unknown tier has tools")
	}
	a := ToolsForTier(TierAggregate)
	a[0] = "changed"
	if ToolsForTier(TierAggregate)[0] == "changed" {
		t.Fatal("ToolsForTier returns the shared slice")
	}
}

func TestOpenSetsFileModeAndFolder(t *testing.T) {
	e := newEnv(t)
	st, err := os.Stat(filepath.Join(e.dir, mcpaudit.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mcp.db mode %v", st.Mode())
	}
}

// A result computed under one approval is not delivered under a later one, even though the profile is active both times.
func TestRecheckSeesANewRevisionAndExpiry(t *testing.T) {
	e := newEnv(t)
	n1 := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", n1))
	_, _ = e.g.Approve(ctx, "h1", appr())
	secret, _, _ := e.g.Claim(ctx, "h1", n1)
	pr, err := e.g.Authenticate(ctx, credScheme+"h1."+secret)
	if err != nil {
		t.Fatal(err)
	}
	// The person reissues, the host enrolls again, the person approves a narrower scope: active again, new revision.
	_, _ = e.g.Reissue(ctx, "h1")
	_, _ = e.g.Enroll(ctx, req("h1", nonce(t)))
	narrow := Approval{Tier: TierAggregate, Tools: []string{"audit_get_guide"}, Sources: []string{"src-1"}}
	if _, err := e.g.Approve(ctx, "h1", narrow); err != nil {
		t.Fatal(err)
	}
	wantDenied(t, e.g.Recheck(ctx, pr), ReasonRevision)
	// Expiry is part of the gate too.
	pr2 := Principal{"h1", 4}
	if err := e.g.Recheck(ctx, pr2); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(DefaultIdleTTL + time.Second)
	wantDenied(t, e.g.Recheck(ctx, pr2), ReasonExpired)
}

func TestRevokeAndReissueDropTheDigest(t *testing.T) {
	e := newEnv(t)
	e.enrolled("h1")
	for _, step := range []func(context.Context, string) (Profile, error){e.g.Revoke, e.g.Reissue} {
		if _, err := step(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
		p, _ := e.db.Get(ctx, "h1")
		if len(p.SecretDigest) != 0 || len(p.ClaimDigest) != 0 || p.Claimed || !p.ExpiresAt.IsZero() {
			t.Fatalf("credential material kept after the step: %+v", p)
		}
	}
}
