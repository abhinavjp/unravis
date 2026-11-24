package mcpsrv

import (
	"context"
	"errors"
	"slices"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-349: evidence aliases (spec 03 D12). A host without the identifiers grant never gets the audit record id of a row. It gets an alias
// for the row, bound to (profile, revision, source), and the person resolves it in the app. Three things end an alias: its own expiry
// (7 days idle, 30 at most), any change of the profile's revision (revoke, reissue, a new approval: the revision changes each time,
// and a profile that was removed and enrolled again starts above the revision floor), and the hooks that drop a profile's aliases on
// revoke, reissue, stop, the master-key lock and the owner's shutdown (the last two for every host). The resolver checks the grant as it is now, so a failed delete is not a way to keep an alias
// alive.

// ErrAliasNotFound is the one answer for every alias that cannot be used.
var ErrAliasNotFound = mcpaudit.ErrAliasNotFound

// Evidence is the record an alias stands for.
type Evidence = mcpaudit.Evidence

// Aliases is the alias store over the grants. It is safe for concurrent use.
type Aliases struct {
	db     *mcpaudit.DB
	grants *Grants
	now    func() time.Time
	logf   func(string, ...any)
	// beforeMint is a test seam, run between the stop check and the mint (nil in the app).
	beforeMint func()
}

// NewAliases builds the alias store and hooks it to the grants, so the aliases of a profile are deleted when its access ends.
func NewAliases(db *mcpaudit.DB, g *Grants, now func() time.Time, logf func(string, ...any)) *Aliases {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	a := &Aliases{db: db, grants: g, now: now, logf: logf}
	g.OnRevoke(a.Drop)
	return a
}

// Drop deletes every alias of a profile. A failure is logged and does not matter for safety: Resolve checks the grant itself, and the
// next prune deletes rows of a profile that is not active at their revision.
func (a *Aliases) Drop(profileID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.db.DropAliases(ctx, profileID); err != nil {
		a.logf("mcp: the evidence references of a profile could not be deleted")
	}
}

// Minter is the mint function of a host's bridge. The profile and revision are fixed in it (the backend builds a new bridge when either
// changes). Every source must be one the grant covers; the mint itself refuses when the profile is not active at the revision.
func (a *Aliases) Minter(p Profile) mcp.EvidenceMinter {
	id, rev, sources := p.ID, p.Rev, slices.Clone(p.Sources)
	return func(ctx context.Context, keys []mcp.EvidenceKey) ([]string, error) {
		ks := make([]mcpaudit.EvidenceKey, len(keys))
		for i, k := range keys {
			if !slices.Contains(sources, k.SourceID) {
				return nil, ErrAliasNotFound
			}
			ks[i] = mcpaudit.EvidenceKey{SourceID: k.SourceID, RecordID: k.RecordID}
		}
		if a.grants.Stopped() {
			return nil, ErrAliasNotFound
		}
		if a.beforeMint != nil {
			a.beforeMint()
		}
		out, err := a.db.MintAliases(ctx, id, rev, ks, a.now())
		// A global stop sets its flag before it deletes the aliases. A mint that committed after that delete would leave aliases that
		// work again after Resume, so a mint that sees the flag after its commit takes its aliases back.
		if err == nil && a.grants.Stopped() {
			a.Drop(id)
			return nil, ErrAliasNotFound
		}
		return out, err
	}
}

// Resolve is the check a host's own presentation of an alias goes through: the alias must belong to this profile at this revision, and
// the grant must still be in force (not revoked, stopped or reissued, and the source still in scope). No tool takes an alias today; this
// is the binding for the one that will.
func (a *Aliases) Resolve(ctx context.Context, pr Principal, alias string) (Evidence, error) {
	e, err := a.db.GetAlias(ctx, alias, a.now())
	if err != nil {
		return Evidence{}, err
	}
	if e.ProfileID != pr.ProfileID || e.PolicyRev != pr.Rev {
		return Evidence{}, ErrAliasNotFound
	}
	p, err := a.grants.Scope(ctx, pr)
	if err != nil || !slices.Contains(p.Sources, e.SourceID) {
		return Evidence{}, ErrAliasNotFound
	}
	return e, nil
}

// ResolveOwner is the resolver of the app's own route (the person pasting an alias). It needs no host credential, but the host's grant
// must still be what it was when the alias was made: the profile is active at that revision, the global stop is off and the source is
// still in its scope. An idle expiry of the host does not end the alias (the person may read the host's report the next day; Renew keeps
// the revision). Every failure is ErrAliasNotFound.
func (a *Aliases) ResolveOwner(ctx context.Context, alias string) (Evidence, error) {
	now := a.now()
	e, err := a.db.GetAlias(ctx, alias, now)
	if err != nil {
		return Evidence{}, err
	}
	p, err := a.grants.db.Get(ctx, e.ProfileID)
	if err != nil {
		if errors.Is(err, mcpaudit.ErrNotFound) {
			return Evidence{}, ErrAliasNotFound
		}
		return Evidence{}, err
	}
	if p.State != mcpaudit.StateActive || p.Rev != e.PolicyRev || a.grants.Stopped() || !slices.Contains(p.Sources, e.SourceID) {
		return Evidence{}, ErrAliasNotFound
	}
	_ = a.db.TouchAlias(ctx, alias, now)
	return e, nil
}
