package mcpaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// States of a profile (spec 03 section 3.4).
const (
	StatePending = "pending"
	StateActive  = "active"
	StateRevoked = "revoked"
	StateDenied  = "denied"
)

// ErrNotFound is returned when a profile id is not in the store.
var ErrNotFound = errors.New("mcpaudit: no such profile")

// ErrExists is returned by Insert for a taken id.
var ErrExists = errors.New("mcpaudit: profile exists")

// Profile is one host installation's row. The digests are keyed digests (DB.MAC), never the secret or the nonce.
type Profile struct {
	ID                   string
	State                string
	Rev                  int
	HostName             string // reported by the host: advisory
	HostVersion          string
	Exe                  string // reported by the bridge: advisory until the pipe peer image check (BL-345)
	PPID                 int
	Tier                 string
	Tools                []string
	Sources              []string
	Identifiers          bool
	IdentifiersUntil     time.Time
	RequestedIdentifiers bool
	IdleTTL              time.Duration
	ExpiresAt            time.Time
	ApprovedAt           time.Time
	CreatedAt            time.Time
	LastCallAt           time.Time
	Claimed              bool
	SecretDigest         []byte `json:"-"` // never serialised: a profile is shown to the screen as is
	ClaimDigest          []byte `json:"-"`
}

const cols = `id,state,rev,host_name,host_version,exe,ppid,tier,tools,sources,identifiers,identifiers_until,requested_identifiers,idle_ttl_s,expires_at,approved_at,created_at,last_call_at,claimed,secret_digest,claim_digest`

type scanner interface{ Scan(...any) error }

func tm(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func scan(s scanner) (Profile, error) {
	var p Profile
	var tools, sources string
	var ident, reqIdent, claimed int
	var identUntil, idle, exp, appr, created, last int64
	if err := s.Scan(&p.ID, &p.State, &p.Rev, &p.HostName, &p.HostVersion, &p.Exe, &p.PPID, &p.Tier, &tools, &sources, &ident, &identUntil,
		&reqIdent, &idle, &exp, &appr, &created, &last, &claimed, &p.SecretDigest, &p.ClaimDigest); err != nil {
		return Profile{}, err
	}
	if err := json.Unmarshal([]byte(tools), &p.Tools); err != nil {
		return Profile{}, errors.New("mcpaudit: bad tools column")
	}
	if err := json.Unmarshal([]byte(sources), &p.Sources); err != nil {
		return Profile{}, errors.New("mcpaudit: bad sources column")
	}
	p.Identifiers, p.RequestedIdentifiers, p.Claimed = ident != 0, reqIdent != 0, claimed != 0
	p.IdentifiersUntil, p.IdleTTL = tm(identUntil), time.Duration(idle)*time.Second
	p.ExpiresAt, p.ApprovedAt, p.CreatedAt, p.LastCallAt = tm(exp), tm(appr), tm(created), tm(last)
	return p, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func args(p Profile) []any {
	tools, _ := json.Marshal(nonNil(p.Tools))
	sources, _ := json.Marshal(nonNil(p.Sources))
	return []any{p.ID, p.State, p.Rev, p.HostName, p.HostVersion, p.Exe, p.PPID, p.Tier, string(tools), string(sources), b2i(p.Identifiers),
		ms(p.IdentifiersUntil), b2i(p.RequestedIdentifiers), int64(p.IdleTTL / time.Second), ms(p.ExpiresAt), ms(p.ApprovedAt), ms(p.CreatedAt),
		ms(p.LastCallAt), b2i(p.Claimed), p.SecretDigest, p.ClaimDigest}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Get returns one profile.
func (d *DB) Get(ctx context.Context, id string) (Profile, error) {
	p, err := scan(d.sql.QueryRowContext(ctx, "SELECT "+cols+" FROM profiles WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// List returns every profile, oldest first, at most limit.
func (d *DB) List(ctx context.Context, limit int) ([]Profile, error) {
	rows, err := d.sql.QueryContext(ctx, "SELECT "+cols+" FROM profiles ORDER BY created_at, id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Profile
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Count returns the number of profiles.
func (d *DB) Count(ctx context.Context) (n int, err error) {
	err = d.sql.QueryRowContext(ctx, "SELECT count(*) FROM profiles").Scan(&n)
	return
}

// Insert adds a new profile; ErrExists if the id is taken.
func (d *DB) Insert(ctx context.Context, p Profile) error {
	_, err := d.sql.ExecContext(ctx, "INSERT INTO profiles("+cols+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", args(p)...)
	if err != nil {
		if _, gerr := d.Get(ctx, p.ID); gerr == nil {
			return ErrExists
		}
		return err
	}
	return nil
}

// Update reads the profile, lets fn change it, and writes it back in one transaction, so two changes cannot interleave. If fn returns
// an error nothing is written and the error is returned as is.
func (d *DB) Update(ctx context.Context, id string, fn func(*Profile) error) (Profile, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scan(tx.QueryRowContext(ctx, "SELECT "+cols+" FROM profiles WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	if err := fn(&p); err != nil {
		return Profile{}, err
	}
	a := args(p)
	// id is the key and does not change; the other twenty columns are set.
	if _, err := tx.ExecContext(ctx, `UPDATE profiles SET state=?,rev=?,host_name=?,host_version=?,exe=?,ppid=?,tier=?,tools=?,sources=?,identifiers=?,
identifiers_until=?,requested_identifiers=?,idle_ttl_s=?,expires_at=?,approved_at=?,created_at=?,last_call_at=?,claimed=?,secret_digest=?,claim_digest=? WHERE id=?`,
		append(a[1:], p.ID)...); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Delete removes a profile row.
func (d *DB) Delete(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx, "DELETE FROM profiles WHERE id=?", id)
	return err
}
