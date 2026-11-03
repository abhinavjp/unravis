package casestore

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"strings"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// MaxCookieNames caps the cookie allowlist per case. A W3C row already has 10
// static attrs plus "client-ip" and "referer", and evidence.MaxEventAttrs is
// 16, so 4 cookie attrs is the most that always fit.
const MaxCookieNames = 4

// MaxCookieNameLen is the longest a single cookie name may be.
const MaxCookieNameLen = 48

// CookieKeyBytes is the length of the HMAC key used to digest cookie values.
const CookieKeyBytes = 32

// ErrBadCookieName is returned for a cookie name outside the allowed charset
// or length.
var ErrBadCookieName = errors.New("casestore: cookie names may use a-z, 0-9, '_', '.', '-' and be 1-48 characters")

// CleanCookieNames trims, lower-cases and de-duplicates names (first spelling
// wins), checks each kept name against the allowed charset and length, and
// returns them sorted ascending. nil or empty input returns an empty non-nil
// slice and nil error. ErrCapExceeded past MaxCookieNames.
func CleanCookieNames(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		if len(n) > MaxCookieNameLen {
			return nil, ErrBadCookieName
		}
		// A name is written into a case export as part of an attribute key,
		// where wording that reads as a claim would fail the file.
		if !evidence.ClaimFree(n) {
			return nil, ErrBadCookieName
		}
		for _, r := range n {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
				return nil, ErrBadCookieName
			}
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) > MaxCookieNames {
		return nil, ErrCapExceeded
	}
	sort.Strings(out)
	return out, nil
}

// CookiePolicyStore is implemented by a Case that keeps a cookie-name
// allowlist and the key used to digest their values. It is a separate
// interface, like LedgerBlobStore, so it breaks no Case.
type CookiePolicyStore interface {
	// CookiePolicy returns the zero importer.CookiePolicy (nil Allowed, nil
	// HMACKey) when the allowlist is empty or was never set; it never creates
	// or writes anything.
	CookiePolicy(ctx context.Context) (importer.CookiePolicy, error)
	// SetCookieAllowlist cleans names with CleanCookieNames, replaces the
	// stored list and returns the cleaned list. It creates the 32-byte key
	// from crypto/rand the first time a non-empty list is stored; an empty
	// list clears the allowlist but keeps any existing key so re-enabling
	// gives the same digests. Refused with ErrCaseClosed once the case is closed.
	SetCookieAllowlist(ctx context.Context, names []string) ([]string, error)
}

// NewCookieKey returns CookieKeyBytes bytes read from crypto/rand. A short
// read is an error.
func NewCookieKey() ([]byte, error) {
	key := make([]byte, CookieKeyBytes)
	n, err := rand.Read(key)
	if err != nil {
		return nil, err
	}
	if n != CookieKeyBytes {
		return nil, errors.New("casestore: short read generating cookie key")
	}
	return key, nil
}

var _ CookiePolicyStore = (*memCase)(nil)

func (c *memCase) CookiePolicy(ctx context.Context) (importer.CookiePolicy, error) {
	if err := c.check(ctx); err != nil {
		return importer.CookiePolicy{}, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	if len(c.d.cookieAllow) == 0 {
		return importer.CookiePolicy{}, nil
	}
	return importer.CookiePolicy{
		Allowed: append([]string(nil), c.d.cookieAllow...),
		HMACKey: append([]byte(nil), c.d.cookieKey...),
	}, nil
}

func (c *memCase) SetCookieAllowlist(ctx context.Context, names []string) ([]string, error) {
	clean, err := CleanCookieNames(names)
	if err != nil {
		return nil, err
	}
	if err := c.tagWrite(ctx); err != nil {
		return nil, err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if c.d.erased {
		return nil, ErrNotFound
	}
	if c.d.info.Closed != nil {
		return nil, ErrCaseClosed
	}
	c.d.cookieAllow = clean
	if len(clean) > 0 && len(c.d.cookieKey) == 0 {
		key, err := NewCookieKey()
		if err != nil {
			return nil, err
		}
		c.d.cookieKey = key
	}
	return append([]string(nil), clean...), nil
}
