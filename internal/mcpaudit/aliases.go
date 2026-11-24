package mcpaudit

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Evidence aliases (BL-349, spec 03 D12). A host without the identifiers grant never sees the audit record id of a row: it sees an alias,
// "ev1_" and 26 random base32 characters (130 bits from crypto/rand, nothing derived from the record id), and the person can resolve it
// in the app to the row it stands for. The alias is bound to the profile, the profile's revision (so a revoke, a reissue and a new
// approval each end it), the source of the row and an expiry (7 days from the last use, 30 days at most).
//
// The record id is kept in the file only sealed (AES-256-GCM under a key derived from the server key, with the alias, profile and source
// as associated data), and the lookup key that finds an existing alias for the same row is a keyed digest: a copy of mcp.db on another
// account holds neither an audit record id nor a way to test a guess of one.

// Alias limits. The numbers are defaults (spec 03 D20).
const (
	AliasIdle            = 7 * 24 * time.Hour  // an alias lives this long after its last use
	AliasCap             = 30 * 24 * time.Hour // and never longer than this from the day it was made
	MaxAliasesPerProfile = 2000                // the oldest (by last use) of a profile's aliases are deleted beyond this
	MaxAliasBatch        = 100                 // refs of one mint call (a page is at most 100 rows)
	aliasPrefix          = "ev1_"
	aliasRandom          = 26 // base32 characters
	maxRecordIDLen       = 128
	maxSourceIDLen       = 128
)

// AliasRE is the shape of every alias this package makes.
var AliasRE = regexp.MustCompile(`^ev1_[a-z2-7]{26}$`)

var (
	// ErrAliasNotFound is the one answer for an alias that does not exist, ended, expired or belongs to a profile that is not active at
	// that revision. The caller cannot tell the cases apart.
	ErrAliasNotFound = errors.New("mcpaudit: no such evidence reference")
	// ErrAliasRefused is a mint that was not done: the profile is not active at the revision, or a ref is empty or too long. Nothing was
	// written.
	ErrAliasRefused = errors.New("mcpaudit: the evidence reference was not made")
)

// EvidenceKey names one audit record of one source.
type EvidenceKey struct {
	SourceID string
	RecordID string
}

// Evidence is what an alias stands for.
type Evidence struct {
	Alias     string
	ProfileID string
	SourceID  string
	RecordID  string
	PolicyRev int
	CreatedAt time.Time
	ExpiresAt time.Time
}

const aliasSchemaSQL = `
CREATE TABLE IF NOT EXISTS aliases(
  alias TEXT PRIMARY KEY CHECK (length(alias) = 30),
  profile_id TEXT NOT NULL,
  source_id TEXT NOT NULL,
  record BLOB NOT NULL,
  rec_key BLOB NOT NULL,
  policy_rev INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  last_used INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS alias_rec ON aliases(profile_id, policy_rev, rec_key);
CREATE INDEX IF NOT EXISTS alias_profile ON aliases(profile_id, last_used);
`

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newAlias() (string, error) {
	var raw [17]byte // 136 bits; 26 characters carry 130 of them
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("mcpaudit: no random source")
	}
	return aliasPrefix + strings.ToLower(b32.EncodeToString(raw[:])[:aliasRandom]), nil
}

// aliasAEAD is the cipher that seals record ids. Its key is derived from the server key, which is itself sealed for the current user.
func (d *DB) aliasAEAD() (cipher.AEAD, error) {
	block, err := aes.NewCipher(d.MAC("alias-seal", nil)) // 32 bytes: AES-256
	if err != nil {
		return nil, errors.New("mcpaudit: sealing is not available")
	}
	return cipher.NewGCM(block)
}

func aliasAAD(alias, profile, source string) []byte {
	return []byte(alias + "\x00" + profile + "\x00" + source)
}

func (d *DB) recKey(profile string, rev int, source, record string) []byte {
	return d.MAC("alias-rec", []byte(profile+"\x00"+strconv.Itoa(rev)+"\x00"+source+"\x00"+record))
}

// MintAliases returns one alias for each key, in order, for the profile at the revision. A key that already has a live alias of this
// profile and revision gets the same one back (and its idle clock restarts); the others get a new random alias. It is one transaction:
// the profile must be active at exactly that revision when it commits, or nothing is written (ErrAliasRefused), so a revoke that won
// the race leaves no alias behind. A profile keeps at most MaxAliasesPerProfile; the least recently used go first.
func (d *DB) MintAliases(ctx context.Context, profileID string, rev int, keys []EvidenceKey, now time.Time) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if len(keys) > MaxAliasBatch {
		return nil, ErrAliasRefused
	}
	for _, k := range keys {
		if k.SourceID == "" || k.RecordID == "" || len(k.SourceID) > maxSourceIDLen || len(k.RecordID) > maxRecordIDLen {
			return nil, ErrAliasRefused
		}
	}
	aead, err := d.aliasAEAD()
	if err != nil {
		return nil, err
	}
	d.amu.Lock() // one alias writer at a time: the transaction below reads, then writes
	defer d.amu.Unlock()
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("mcpaudit: the evidence reference was not made")
	}
	defer func() { _ = tx.Rollback() }()
	nowMs, capMs := ms(now), ms(now.Add(-AliasCap))
	// A write first, so the transaction holds the write lock before it reads (a read-then-write transaction can fail with a stale snapshot).
	if _, err := tx.ExecContext(ctx, `DELETE FROM aliases WHERE profile_id=? AND (expires_at<=? OR created_at<=?)`, profileID, nowMs, capMs); err != nil {
		return nil, errors.New("mcpaudit: the evidence reference was not made")
	}
	var state string
	var curRev int
	if err := tx.QueryRowContext(ctx, `SELECT state,rev FROM profiles WHERE id=?`, profileID).Scan(&state, &curRev); err != nil || state != StateActive || curRev != rev {
		return nil, ErrAliasRefused
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		rk := d.recKey(profileID, rev, k.SourceID, k.RecordID)
		var alias string
		switch err := tx.QueryRowContext(ctx, `SELECT alias FROM aliases WHERE profile_id=? AND policy_rev=? AND rec_key=?`, profileID, rev, rk).Scan(&alias); {
		case err == nil:
			if _, err := tx.ExecContext(ctx, `UPDATE aliases SET last_used=?, expires_at=min(?, created_at+?) WHERE alias=?`,
				nowMs, ms(now.Add(AliasIdle)), AliasCap.Milliseconds(), alias); err != nil {
				return nil, errors.New("mcpaudit: the evidence reference was not made")
			}
		case errors.Is(err, sql.ErrNoRows):
			if alias, err = newAlias(); err != nil {
				return nil, err
			}
			sealed, serr := sealRecord(aead, alias, profileID, k)
			if serr != nil {
				return nil, serr
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO aliases(alias,profile_id,source_id,record,rec_key,policy_rev,created_at,last_used,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				alias, profileID, k.SourceID, sealed, rk, rev, nowMs, nowMs, ms(now.Add(AliasIdle))); err != nil {
				return nil, errors.New("mcpaudit: the evidence reference was not made")
			}
		default:
			return nil, errors.New("mcpaudit: the evidence reference was not made")
		}
		out[i] = alias
	}
	// Over the cap, the aliases used least recently go. The ones just made or touched have the newest last_used, so they stay while the
	// batch is smaller than the cap (it is: MaxAliasBatch).
	if _, err := tx.ExecContext(ctx, `DELETE FROM aliases WHERE profile_id=? AND alias IN (SELECT alias FROM aliases WHERE profile_id=? ORDER BY last_used DESC, created_at DESC, alias LIMIT -1 OFFSET ?)`,
		profileID, profileID, MaxAliasesPerProfile); err != nil {
		return nil, errors.New("mcpaudit: the evidence reference was not made")
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("mcpaudit: the evidence reference was not made")
	}
	return out, nil
}

func sealRecord(aead cipher.AEAD, alias, profile string, k EvidenceKey) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("mcpaudit: no random source")
	}
	return aead.Seal(nonce, nonce, []byte(k.RecordID), aliasAAD(alias, profile, k.SourceID)), nil
}

// GetAlias reads one alias. It answers ErrAliasNotFound for a string that is not an alias, a row that is not there or past its expiry
// or cap, and a row whose sealed record id does not open (a row copied or changed in the file). It does not check the profile: that is
// the caller's, who holds the grants (mcpsrv.Aliases). A successful read restarts the idle clock, never past the cap.
func (d *DB) GetAlias(ctx context.Context, alias string, now time.Time) (Evidence, error) {
	if !AliasRE.MatchString(alias) {
		return Evidence{}, ErrAliasNotFound
	}
	aead, err := d.aliasAEAD()
	if err != nil {
		return Evidence{}, err
	}
	d.amu.Lock()
	defer d.amu.Unlock()
	var e Evidence
	var sealed []byte
	var created, expires int64
	err = d.sql.QueryRowContext(ctx, `SELECT profile_id,source_id,record,policy_rev,created_at,expires_at FROM aliases WHERE alias=?`, alias).
		Scan(&e.ProfileID, &e.SourceID, &sealed, &e.PolicyRev, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, ErrAliasNotFound
	}
	if err != nil {
		return Evidence{}, errors.New("mcpaudit: the evidence reference could not be read")
	}
	e.Alias, e.CreatedAt, e.ExpiresAt = alias, tm(created), tm(expires)
	if !now.Before(e.ExpiresAt) || !now.Before(e.CreatedAt.Add(AliasCap)) {
		return Evidence{}, ErrAliasNotFound
	}
	ns := aead.NonceSize()
	if len(sealed) <= ns {
		return Evidence{}, ErrAliasNotFound
	}
	rec, err := aead.Open(nil, sealed[:ns], sealed[ns:], aliasAAD(alias, e.ProfileID, e.SourceID))
	if err != nil {
		return Evidence{}, ErrAliasNotFound
	}
	e.RecordID = string(rec)
	return e, nil
}

// TouchAlias restarts the idle clock of an alias that was resolved for a person (never past the cap). A failure is not the resolve's
// failure; the caller ignores it.
func (d *DB) TouchAlias(ctx context.Context, alias string, now time.Time) error {
	d.amu.Lock()
	defer d.amu.Unlock()
	_, err := d.sql.ExecContext(ctx, `UPDATE aliases SET last_used=?, expires_at=min(?, created_at+?) WHERE alias=?`,
		ms(now), ms(now.Add(AliasIdle)), AliasCap.Milliseconds(), alias)
	return err
}

// DropAliases deletes every alias of a profile (revoke, reissue, stop, remove).
func (d *DB) DropAliases(ctx context.Context, profileID string) error {
	d.amu.Lock()
	defer d.amu.Unlock()
	_, err := d.sql.ExecContext(ctx, `DELETE FROM aliases WHERE profile_id=?`, profileID)
	return err
}

// CountAliases returns how many aliases a profile has (all profiles when profileID is empty).
func (d *DB) CountAliases(ctx context.Context, profileID string) (n int, err error) {
	if profileID == "" {
		err = d.sql.QueryRowContext(ctx, `SELECT count(*) FROM aliases`).Scan(&n)
	} else {
		err = d.sql.QueryRowContext(ctx, `SELECT count(*) FROM aliases WHERE profile_id=?`, profileID).Scan(&n)
	}
	return n, err
}

// pruneAliases deletes the aliases that are past their expiry or cap, and those of a profile that is gone or is no longer at the
// revision they were made under (a revoke whose hook did not run, a race with a mint that lost).
func (d *DB) pruneAliases(ctx context.Context, now time.Time) (int64, error) {
	d.amu.Lock()
	defer d.amu.Unlock()
	r, err := d.sql.ExecContext(ctx, `DELETE FROM aliases WHERE expires_at<=? OR created_at<=? OR NOT EXISTS (SELECT 1 FROM profiles p WHERE p.id=aliases.profile_id AND p.state='active' AND p.rev=aliases.policy_rev)`,
		ms(now), ms(now.Add(-AliasCap)))
	if err != nil {
		return 0, errors.New("mcpaudit: the evidence references could not be pruned")
	}
	return r.RowsAffected()
}
