package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

var _ casestore.CookiePolicyStore = (*Case)(nil)

// The cookie allowlist and the HMAC key that digests the allowed values live in
// two rows of the existing meta table (no schema change). The key is never
// written to the manifest, a log, an error or an export.
const (
	cookieAllowKey = "cookie.allowlist"
	cookieKeyKey   = "cookie.hmac_key_v1"
)

// CookiePolicy reads the stored policy. It never writes: a case with no
// allowlist, or an empty one, gives the zero policy and no key is created.
func (c *Case) CookiePolicy(ctx context.Context) (importer.CookiePolicy, error) {
	if err := ctx.Err(); err != nil {
		return importer.CookiePolicy{}, err
	}
	db, err := c.handle()
	if err != nil {
		return importer.CookiePolicy{}, err
	}
	allowRaw, _, err := metaValue(ctx, db, cookieAllowKey)
	if err != nil {
		return importer.CookiePolicy{}, err
	}
	var stored []string
	if allowRaw != "" {
		if err := json.Unmarshal([]byte(allowRaw), &stored); err != nil {
			return importer.CookiePolicy{}, fmt.Errorf("%w: cookie allowlist", ErrInvalid)
		}
	}
	names, err := casestore.CleanCookieNames(stored)
	if err != nil {
		return importer.CookiePolicy{}, fmt.Errorf("%w: cookie allowlist", ErrInvalid)
	}
	if len(names) == 0 {
		return importer.CookiePolicy{}, nil
	}
	keyRaw, found, err := metaValue(ctx, db, cookieKeyKey)
	if err != nil {
		return importer.CookiePolicy{}, err
	}
	if !found {
		return importer.CookiePolicy{}, fmt.Errorf("%w: cookie key missing", ErrInvalid)
	}
	key, err := hex.DecodeString(keyRaw)
	if err != nil || len(key) != casestore.CookieKeyBytes {
		return importer.CookiePolicy{}, fmt.Errorf("%w: cookie key", ErrInvalid)
	}
	return importer.CookiePolicy{Allowed: names, HMACKey: key}, nil
}

// metaValue reads one meta row; found is false when the row is absent.
func metaValue(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, k string) (v string, found bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT v FROM meta WHERE k=?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetCookieAllowlist stores the cleaned list and, the first time a non-empty
// list is stored, a new 32-byte key. An existing key is never replaced, and a
// call that changes nothing writes nothing.
func (c *Case) SetCookieAllowlist(ctx context.Context, names []string) ([]string, error) {
	clean, err := casestore.CleanCookieNames(names)
	if err != nil {
		return nil, err
	}
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, c.mapDBErr(err)
	}
	current, found, err := metaValue(ctx, tx, cookieAllowKey)
	if err != nil {
		_ = tx.Rollback()
		return nil, c.mapDBErr(err)
	}
	if !found {
		current = "[]"
	}
	_, keyExists, err := metaValue(ctx, tx, cookieKeyKey)
	if err != nil {
		_ = tx.Rollback()
		return nil, c.mapDBErr(err)
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	want := string(encoded)
	changed := want != current || (len(clean) > 0 && !keyExists)
	if changed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, cookieAllowKey, want); err != nil {
			_ = tx.Rollback()
			return nil, c.mapDBErr(err)
		}
		if len(clean) > 0 && !keyExists {
			key, err := casestore.NewCookieKey()
			if err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?)`, cookieKeyKey, hex.EncodeToString(key)); err != nil {
				_ = tx.Rollback()
				return nil, c.mapDBErr(err)
			}
		}
		if err := bumpCaseRevision(ctx, tx); err != nil {
			_ = tx.Rollback()
			return nil, c.mapDBErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, c.mapDBErr(err)
	}
	return append([]string(nil), clean...), nil
}
