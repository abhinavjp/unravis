package mcpsrv

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/mcpaudit"
)

// Profile is one host installation (spec 03 section 3.4).
type Profile = mcpaudit.Profile

// Defaults and bounds (spec 03 D11, D25, section 3.4). The numbers are the spec's; BL-355 may change them.
const (
	MaxProfiles        = 64
	DefaultIdleTTL     = 8 * time.Hour
	MinIdleTTL         = time.Minute
	MaxIdleTTL         = 30 * 24 * time.Hour
	AbsoluteCap        = 30 * 24 * time.Hour // from approval (or the last renew), whatever the idle time
	DefaultIdentifiers = time.Hour
	MaxIdentifiers     = 7 * 24 * time.Hour
	ClaimTTL           = 10 * time.Minute
	touchEvery         = time.Minute // LastCallAt and the sliding expiry are written at most this often

	maxHostName, maxHostVersion, maxExe = 64, 32, 520
	maxSources, maxSourceID             = 256, 128
	nonceLen                            = 22 // 16 random bytes, base64url, no padding
	secretLen                           = 43 // 32 random bytes, base64url, no padding

	credScheme = "Unravis-Grant " //nolint:gosec // G101: the name of the Authorization scheme, not a credential
	macGrant   = "grant"
	macClaim   = "claim"
	metaStop   = "stopped"
)

// DefaultProfileID is the profile of a legacy `--mcp` with no `--mcp-profile` (D11).
const DefaultProfileID = "default"

var (
	idRe    = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	b64urRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ErrAccessEnded is the cancel cause of a call whose profile was revoked, stopped, reissued or locked while it ran.
var ErrAccessEnded = errors.New("mcpsrv: access ended while the call ran")

// Approval is what the person chose on the approval card.
type Approval struct {
	Tier           string        // TierAggregate or TierEvidence
	Tools          []string      // subset of the tier's tools; empty means all of the tier
	Sources        []string      // explicit source ids (the UI expands "all connected now"); at least one
	Identifiers    bool          // the identifiers grant; never taken from the host's request
	IdentifiersFor time.Duration // zero means DefaultIdentifiers; at most MaxIdentifiers
	IdleTTL        time.Duration // zero means DefaultIdleTTL
}

// DefaultApproval is the one-click "Approve with defaults" of D11: tier aggregate, the given sources, 8 h idle expiry, no identifiers.
func DefaultApproval(sources []string) Approval {
	return Approval{Tier: TierAggregate, Sources: sources, IdleTTL: DefaultIdleTTL}
}

// Principal is an authenticated profile at a policy revision. A result is delivered only while Recheck still passes for it.
type Principal struct {
	ProfileID string
	Rev       int
}

// EnrollReq is the body of POST /mcp/v1/enroll. Everything but the profile id and the nonce is advisory text the host or the bridge
// reports about itself; the card shows it as "reported by the host".
type EnrollReq struct {
	Profile            string `json:"profile"`
	HostName           string `json:"hostName"`
	HostVersion        string `json:"hostVersion"`
	Exe                string `json:"exe"`
	PPID               int    `json:"ppid"`
	ClaimNonce         string `json:"claimNonce"`
	RequestIdentifiers bool   `json:"requestIdentifiers"`
}

// EnrollResp is the answer of enroll.
type EnrollResp struct {
	State string `json:"state"`
	Rev   int    `json:"profileRev"`
}

type pendingSecret struct {
	secret string
	until  time.Time
}

// Grants is the profile and credential store with its rules. One value serves the backend; every method is safe for concurrent use.
type Grants struct {
	db  *mcpaudit.DB
	now func() time.Time

	mu       sync.Mutex // serialises every change and guards the three fields below
	secrets  map[string]pendingSecret
	running  map[string]map[uint64]context.CancelCauseFunc
	nextRun  uint64
	onRevoke []func(profileID string)

	stopped atomic.Bool
}

// NewGrants opens the rules over a store. The global stop survives a restart.
func NewGrants(ctx context.Context, db *mcpaudit.DB, now func() time.Time) (*Grants, error) {
	if now == nil {
		now = time.Now
	}
	g := &Grants{db: db, now: now, secrets: map[string]pendingSecret{}, running: map[string]map[uint64]context.CancelCauseFunc{}}
	v, ok, err := db.Meta(ctx, metaStop)
	if err != nil {
		return nil, errors.New("mcpsrv: reading the stop flag failed")
	}
	g.stopped.Store(ok && string(v) == "1")
	return g, nil
}

// OnRevoke registers a hook called (outside the lock) after a profile's access ended: revoke, reissue, stop (once per profile) and
// CancelAll. Handles, cursors and evidence aliases (BL-349, BL-350) drop the profile's state here.
func (g *Grants) OnRevoke(fn func(profileID string)) {
	g.mu.Lock()
	g.onRevoke = append(g.onRevoke, fn)
	g.mu.Unlock()
}

// Stopped reports the global stop.
func (g *Grants) Stopped() bool { return g.stopped.Load() }

// ---- enrollment ----------------------------------------------------------------------------------------------------------------

// CreatePending is the person's "Add host" (spec 03 3.6 A step 1): a profile id with no host and no secret yet. An id that exists is
// left as it is (use Reissue).
func (g *Grants) CreatePending(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return badRequest("profile id")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.insertPending(ctx, mcpaudit.Profile{ID: id})
}

func (g *Grants) insertPending(ctx context.Context, p mcpaudit.Profile) error {
	n, err := g.db.Count(ctx)
	if err != nil {
		return err
	}
	if n >= MaxProfiles {
		return ErrTooManyProfiles
	}
	p.State, p.Rev, p.CreatedAt = mcpaudit.StatePending, 1, g.now().UTC()
	return g.db.Insert(ctx, p)
}

// Enroll is the first call of a host. A new profile id (including the default one of a legacy `--mcp`) becomes pending and is never
// approved by this call. The claim nonce is kept, as a keyed digest, on the first enroll only: a second process that learns the
// profile id cannot replace it and claim the credential the person approves. If the first bridge is gone, the person uses Reissue.
func (g *Grants) Enroll(ctx context.Context, r EnrollReq) (EnrollResp, error) {
	if !idRe.MatchString(r.Profile) {
		return EnrollResp{}, badRequest("profile id")
	}
	if !validText(r.HostName, maxHostName) || !validText(r.HostVersion, maxHostVersion) || !validText(r.Exe, maxExe) || r.PPID < 0 {
		return EnrollResp{}, badRequest("host description")
	}
	if len(r.ClaimNonce) != nonceLen || !b64urRe.MatchString(r.ClaimNonce) {
		return EnrollResp{}, badRequest("claim nonce")
	}
	digest := g.db.MAC(macClaim, []byte(r.ClaimNonce))

	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := g.db.Get(ctx, r.Profile)
	switch {
	case errors.Is(err, mcpaudit.ErrNotFound):
		np := mcpaudit.Profile{ID: r.Profile, HostName: r.HostName, HostVersion: r.HostVersion, Exe: r.Exe, PPID: r.PPID,
			RequestedIdentifiers: r.RequestIdentifiers, ClaimDigest: digest}
		if err := g.insertPending(ctx, np); err != nil {
			return EnrollResp{}, err
		}
		return EnrollResp{State: mcpaudit.StatePending, Rev: 1}, nil
	case err != nil:
		return EnrollResp{}, err
	}
	if p.State == mcpaudit.StatePending && (len(p.ClaimDigest) == 0 || subtle.ConstantTimeCompare(p.ClaimDigest, digest) == 1) {
		p, err = g.db.Update(ctx, p.ID, func(p *Profile) error {
			p.HostName, p.HostVersion, p.Exe, p.PPID = r.HostName, r.HostVersion, r.Exe, r.PPID
			p.RequestedIdentifiers = r.RequestIdentifiers
			p.ClaimDigest = digest
			return nil
		})
		if err != nil {
			return EnrollResp{}, err
		}
	}
	return EnrollResp{State: p.State, Rev: p.Rev}, nil
}

// Claim hands the secret of an approved profile to the bridge that enrolled it: once, with the nonce only it holds.
func (g *Grants) Claim(ctx context.Context, id, nonce string) (secret string, rev int, err error) {
	if !idRe.MatchString(id) || len(nonce) != nonceLen || !b64urRe.MatchString(nonce) {
		return "", 0, badRequest("claim")
	}
	digest := g.db.MAC(macClaim, []byte(nonce))
	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := g.db.Get(ctx, id)
	if errors.Is(err, mcpaudit.ErrNotFound) {
		return "", 0, ErrClaimGone
	}
	if err != nil {
		return "", 0, err
	}
	switch p.State {
	case mcpaudit.StatePending:
		if len(p.ClaimDigest) != 0 && subtle.ConstantTimeCompare(p.ClaimDigest, digest) != 1 {
			return "", 0, ErrWrongNonce
		}
		return "", 0, ErrClaimPending
	case mcpaudit.StateActive:
	default:
		return "", 0, ErrClaimGone
	}
	if p.Claimed || len(p.ClaimDigest) == 0 {
		return "", 0, ErrClaimGone
	}
	if subtle.ConstantTimeCompare(p.ClaimDigest, digest) != 1 {
		return "", 0, ErrWrongNonce
	}
	ps, ok := g.secrets[id]
	if !ok || !g.now().Before(ps.until) {
		delete(g.secrets, id)
		return "", 0, ErrClaimGone // the owner restarted or the ten minutes passed: the person uses Reissue
	}
	if _, err := g.db.Update(ctx, id, func(p *Profile) error { p.Claimed, p.ClaimDigest = true, nil; return nil }); err != nil {
		return "", 0, err // the secret stays in memory: the bridge may ask again
	}
	delete(g.secrets, id)
	return ps.secret, p.Rev, nil
}

// ---- the person's decisions (the screen of BL-347 calls these) --------------------------------------------------------------------

// Approve turns a pending profile with a connected host into an active one and makes its credential. The secret is held in memory for
// ClaimTTL for the host's Claim; the store keeps only its digest.
func (g *Grants) Approve(ctx context.Context, id string, a Approval) (Profile, error) {
	if err := validateApproval(&a); err != nil {
		return Profile{}, err
	}
	secret, digest, err := g.newSecret()
	if err != nil {
		return Profile{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().UTC()
	p, err := g.db.Update(ctx, id, func(p *Profile) error {
		if p.State != mcpaudit.StatePending {
			return ErrNotApprovable
		}
		if len(p.ClaimDigest) == 0 {
			return ErrNoHostYet
		}
		p.State, p.Rev = mcpaudit.StateActive, p.Rev+1
		p.Tier, p.Tools, p.Sources = a.Tier, a.Tools, a.Sources
		p.Identifiers = a.Identifiers
		p.IdentifiersUntil = time.Time{}
		if a.Identifiers {
			p.IdentifiersUntil = now.Add(a.IdentifiersFor)
		}
		p.IdleTTL, p.ApprovedAt, p.LastCallAt, p.Claimed = a.IdleTTL, now, time.Time{}, false
		p.ExpiresAt = expiry(now, p.IdleTTL, p.ApprovedAt)
		p.SecretDigest = digest
		return nil
	})
	if err != nil {
		return Profile{}, err
	}
	g.secrets[id] = pendingSecret{secret: secret, until: now.Add(ClaimTTL)}
	return p, nil
}

// Deny turns a pending profile into a denied one.
func (g *Grants) Deny(ctx context.Context, id string) (Profile, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.db.Update(ctx, id, func(p *Profile) error {
		if p.State != mcpaudit.StatePending {
			return ErrNotApprovable
		}
		p.State, p.Rev, p.ClaimDigest = mcpaudit.StateDenied, p.Rev+1, nil
		return nil
	})
}

// Revoke ends a profile's access: the credential digest is dropped (the secret can never match again), running calls are cancelled,
// and hooks clear the profile's handles and aliases. A revoked profile stays revoked across a restart until Reissue.
func (g *Grants) Revoke(ctx context.Context, id string) (Profile, error) {
	return g.end(ctx, id, mcpaudit.StateRevoked)
}

// Reissue is the person's answer to a lost claim or a copied credential: the old digest is dropped, the profile is pending again with
// its scope kept for the card, and the host must enroll with a fresh nonce and be approved again.
func (g *Grants) Reissue(ctx context.Context, id string) (Profile, error) {
	return g.end(ctx, id, mcpaudit.StatePending)
}

func (g *Grants) end(ctx context.Context, id, state string) (Profile, error) {
	g.mu.Lock()
	p, err := g.db.Update(ctx, id, func(p *Profile) error {
		p.State, p.Rev = state, p.Rev+1
		p.SecretDigest, p.ClaimDigest, p.Claimed = nil, nil, false
		p.ExpiresAt = time.Time{}
		return nil
	})
	if err != nil {
		g.mu.Unlock()
		return Profile{}, err
	}
	delete(g.secrets, id)
	cancels, hooks := g.takeRunning(id), slices.Clone(g.onRevoke)
	g.mu.Unlock()
	finish(cancels, hooks, id)
	return p, nil
}

// Renew is the one-click renewal of an expired (or soon to expire) active profile. It restarts the idle clock and the absolute cap;
// the policy revision does not change, because nothing about what the host may do changed.
func (g *Grants) Renew(ctx context.Context, id string) (Profile, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().UTC()
	return g.db.Update(ctx, id, func(p *Profile) error {
		if p.State != mcpaudit.StateActive {
			return ErrNotApprovable
		}
		p.ApprovedAt = now
		p.ExpiresAt = expiry(now, p.IdleTTL, p.ApprovedAt)
		return nil
	})
}

// Remove deletes the row of a revoked or denied profile (the list is capped at MaxProfiles).
func (g *Grants) Remove(ctx context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := g.db.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.State != mcpaudit.StateRevoked && p.State != mcpaudit.StateDenied {
		return ErrNotApprovable
	}
	return g.db.Delete(ctx, id)
}

// Stop is the global stop: every call is refused with ReasonStopped, every running call is cancelled and every profile's hooks run, until Resume. The flag is
// stored, so a restart does not lift it. Profiles keep their state.
func (g *Grants) Stop(ctx context.Context) error {
	g.mu.Lock()
	if err := g.db.SetMeta(ctx, metaStop, []byte("1")); err != nil {
		g.mu.Unlock()
		return err
	}
	g.stopped.Store(true)
	ids, cancels, hooks := g.takeAllRunning()
	// Every profile's handles and aliases go, not only those of profiles with a call running: after Resume nothing from before is usable.
	if all, err := g.db.List(ctx, MaxProfiles); err == nil {
		for _, p := range all {
			if _, ok := cancels[p.ID]; !ok {
				ids = append(ids, p.ID)
			}
		}
	}
	g.mu.Unlock()
	for _, id := range ids {
		finish(cancels[id], hooks, id)
	}
	return nil
}

// Resume lifts the global stop.
func (g *Grants) Resume(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.db.SetMeta(ctx, metaStop, []byte("0")); err != nil {
		return err
	}
	g.stopped.Store(false)
	return nil
}

// CancelAll cancels every running call and runs the hooks, without changing any profile. The case lock and the owner's shutdown use it.
func (g *Grants) CancelAll() {
	g.mu.Lock()
	ids, cancels, hooks := g.takeAllRunning()
	g.mu.Unlock()
	for _, id := range ids {
		finish(cancels[id], hooks, id)
	}
}

// List returns the profiles, oldest first. Digests are not serialised (json:"-").
func (g *Grants) List(ctx context.Context) ([]Profile, error) { return g.db.List(ctx, MaxProfiles) }

// ---- the host's calls (the dispatcher of BL-344 calls these) ----------------------------------------------------------------------

// Authenticate checks the Authorization header of a call: "Unravis-Grant <profile>.<secret>". A profile that is not active answers
// with its reason (the host is told to open the app); an active one must match the stored digest (constant time), then the global
// stop and the idle expiry apply. Every credential failure is the same error, whether the id exists or not.
func (g *Grants) Authenticate(ctx context.Context, header string) (Principal, error) {
	id, secret, ok := parseCredential(header)
	digest := g.db.MAC(macGrant, []byte(secret))
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	p, err := g.db.Get(ctx, id)
	if errors.Is(err, mcpaudit.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	if p.State != mcpaudit.StateActive {
		return Principal{}, denied(p.State)
	}
	if subtle.ConstantTimeCompare(p.SecretDigest, digest) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	now := g.now().UTC()
	if g.stopped.Load() {
		return Principal{}, denied(ReasonStopped)
	}
	if expired(p, now) {
		return Principal{}, denied(ReasonExpired)
	}
	if now.Sub(p.LastCallAt) >= touchEvery {
		g.touch(ctx, p.ID, now)
	}
	return Principal{ProfileID: p.ID, Rev: p.Rev}, nil
}

func (g *Grants) touch(ctx context.Context, id string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// A failed write only means the idle clock is not extended this time; the call itself is allowed.
	_, _ = g.db.Update(ctx, id, func(p *Profile) error {
		if p.State != mcpaudit.StateActive {
			return ErrNotApprovable
		}
		p.LastCallAt = now
		p.ExpiresAt = expiry(now, p.IdleTTL, p.ApprovedAt)
		return nil
	})
}

// Recheck is the revoke gate: a result is emitted only if the profile is still active at the same revision, not stopped, not expired.
func (g *Grants) Recheck(ctx context.Context, pr Principal) error {
	p, err := g.db.Get(ctx, pr.ProfileID)
	if errors.Is(err, mcpaudit.ErrNotFound) {
		return denied(ReasonRevoked)
	}
	if err != nil {
		return err
	}
	switch {
	case p.State != mcpaudit.StateActive:
		return denied(p.State)
	case g.stopped.Load():
		return denied(ReasonStopped)
	case p.Rev != pr.Rev:
		return denied(ReasonRevision)
	case expired(p, g.now().UTC()):
		return denied(ReasonExpired)
	}
	return nil
}

// BeginCall registers a running call of the profile: the returned context ends (cause ErrAccessEnded) when the profile is revoked,
// reissued or stopped, or on CancelAll, and the caller must call done when the call ends. The profile is rechecked after the call is
// registered, so a revoke cannot fall between the check and the registration.
func (g *Grants) BeginCall(ctx context.Context, pr Principal) (context.Context, func(), error) {
	cctx, cancel := context.WithCancelCause(ctx)
	g.mu.Lock()
	g.nextRun++
	n := g.nextRun
	if g.running[pr.ProfileID] == nil {
		g.running[pr.ProfileID] = map[uint64]context.CancelCauseFunc{}
	}
	g.running[pr.ProfileID][n] = cancel
	g.mu.Unlock()
	done := func() {
		g.mu.Lock()
		if m := g.running[pr.ProfileID]; m != nil {
			delete(m, n)
			if len(m) == 0 {
				delete(g.running, pr.ProfileID)
			}
		}
		g.mu.Unlock()
		cancel(nil)
	}
	if err := g.Recheck(ctx, pr); err != nil {
		done()
		return nil, nil, err
	}
	return cctx, done, nil
}

// StateDenial is the denial the bridge shows for a profile state the enroll answer reported.
func StateDenial(state string) *Denied { return denied(state) }

// IdentifiersAllowed says whether the identifiers grant of the profile is in force now (BL-348 reads it on every call).
func IdentifiersAllowed(p Profile, now time.Time) bool {
	return p.Identifiers && now.Before(p.IdentifiersUntil)
}

// ---- helpers ---------------------------------------------------------------------------------------------------------------------

func (g *Grants) newSecret() (secret string, digest []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, errors.New("mcpsrv: no random source")
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	for i := range b {
		b[i] = 0
	}
	return secret, g.db.MAC(macGrant, []byte(secret)), nil
}

func (g *Grants) takeRunning(id string) []context.CancelCauseFunc {
	m := g.running[id]
	delete(g.running, id)
	out := make([]context.CancelCauseFunc, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	return out
}

func (g *Grants) takeAllRunning() (ids []string, cancels map[string][]context.CancelCauseFunc, hooks []func(string)) {
	cancels = map[string][]context.CancelCauseFunc{}
	for id := range g.running {
		ids = append(ids, id)
		cancels[id] = g.takeRunning(id)
	}
	return ids, cancels, slices.Clone(g.onRevoke)
}

func finish(cancels []context.CancelCauseFunc, hooks []func(string), id string) {
	for _, c := range cancels {
		c(ErrAccessEnded)
	}
	for _, h := range hooks {
		h(id)
	}
}

func expiry(now time.Time, idle time.Duration, approved time.Time) time.Time {
	e, c := now.Add(idle), approved.Add(AbsoluteCap)
	if c.Before(e) {
		return c
	}
	return e
}

func expired(p Profile, now time.Time) bool { return !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) }

// parseCredential splits "Unravis-Grant <profile>.<secret>". On a bad form it returns ok=false and an empty secret.
func parseCredential(h string) (id, secret string, ok bool) {
	rest, found := strings.CutPrefix(h, credScheme)
	if !found || len(rest) > 80 {
		return "", "", false
	}
	id, secret, found = strings.Cut(rest, ".")
	if !found || !idRe.MatchString(id) || len(secret) != secretLen || !b64urRe.MatchString(secret) {
		return "", "", false
	}
	return id, secret, true
}

// validText accepts printable UTF-8 of at most max bytes (host text shown on the card: no control characters, no line breaks).
func validText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

func validateApproval(a *Approval) error {
	allowed := ToolsForTier(a.Tier)
	if allowed == nil {
		return ErrBadApproval
	}
	if len(a.Tools) == 0 {
		a.Tools = allowed
	}
	seen := map[string]bool{}
	for _, t := range a.Tools {
		if !slices.Contains(allowed, t) || seen[t] {
			return ErrBadApproval
		}
		seen[t] = true
	}
	if len(a.Sources) == 0 || len(a.Sources) > maxSources {
		return ErrBadApproval
	}
	seenS := map[string]bool{}
	for _, s := range a.Sources {
		if s == "" || !validText(s, maxSourceID) || seenS[s] {
			return ErrBadApproval
		}
		seenS[s] = true
	}
	if a.IdleTTL == 0 {
		a.IdleTTL = DefaultIdleTTL
	}
	if a.IdleTTL < MinIdleTTL || a.IdleTTL > MaxIdleTTL {
		return ErrBadApproval
	}
	if a.IdentifiersFor == 0 {
		a.IdentifiersFor = DefaultIdentifiers
	}
	if a.IdentifiersFor < 0 || a.IdentifiersFor > MaxIdentifiers {
		return ErrBadApproval
	}
	a.Tools, a.Sources = slices.Clone(a.Tools), slices.Clone(a.Sources)
	return nil
}
