// ceiling.go is the C8 rights ceiling: the runner calls CheckCeiling at EVERY
// (re)connect before any sampler runs, and the connection layer (conn.go)
// admits every physical connection through the same check. Two fixed,
// allowlisted control queries prove the login is in no privileged fixed server
// role and holds no EFFECTIVE server permission outside {CONNECT SQL, VIEW ANY
// DATABASE, VIEW SERVER STATE, VIEW SERVER PERFORMANCE STATE}. The permission
// source is sys.fn_my_permissions(NULL, N'SERVER') — effective rights, so
// custom/nested roles, Windows groups, public and SQL2022 ##MS_*## roles are
// all visible (W9-01) — plus a positive readable-proof signal: an empty or
// unresolved result never means "clean". Read-only: the app never grants or
// alters rights, and protective DENYs are already subtracted by the function,
// so no owner is ever told to remove a DENY. Refusal is the fixed
// ErrTooManyRights. Every failure keeps its C8 origin (IsCeilingFailure)
// independently of the public code, so the runner latches immediately (W9-03).
// No goroutines, no tickers.
package bbsql

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ceilingTimeout is the per-control-query budget (two small fixed queries).
const ceilingTimeout = 3 * time.Second

// ErrTooManyRights is the fixed C8 refusal (code too-many-rights).
var ErrTooManyRights = &Error{CodeTooManyRights, "this login has more rights than the Blackbox needs"}

// errCeilingUnreadable fails closed when a control result cannot be read. Its
// PUBLIC code stays the generic failed (C10: no new public surface), but the
// ceilingError wrapper below preserves the C8 origin so the runner takes the
// ceiling-failure path and latches auto-off at once instead of retrying ten
// times (W9-03).
var errCeilingUnreadable = &Error{CodeFailed, "The login rights check could not be completed."}

// ceilingError marks an error as a failed C8 proof, independently of the fixed
// public code it carries. Classify still sees the inner *Error through Unwrap;
// IsCeilingFailure is the seam the runner's failure path uses to latch.
type ceilingError struct{ e *Error }

func (c *ceilingError) Error() string { return c.e.Error() }
func (c *ceilingError) Unwrap() error { return c.e }

// IsCeilingFailure reports whether err came from the C8 rights proof (either
// control query failing, an unreadable result, or an excess-right refusal).
// The runner must treat every such failure as a ceiling failure: immediate
// persisted auto-off, owner-only re-enable (W9-03).
func IsCeilingFailure(err error) bool {
	var c *ceilingError
	return errors.As(err, &c)
}

// ceilingFail wraps a control-query failure, keeping its fixed code and adding
// the C8 origin.
func ceilingFail(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return &ceilingError{e}
	}
	return &ceilingError{Classify(err)}
}

// ceilingAllowed is the complete C8 permission ceiling.
var ceilingAllowed = map[string]bool{
	"CONNECT SQL":                   true,
	"VIEW ANY DATABASE":             true,
	"VIEW SERVER STATE":             true,
	"VIEW SERVER PERFORMANCE STATE": true,
}

// ceilingRoles are the fixed server roles whose membership alone is more than
// the Blackbox needs, so ANY flag of 1 refuses the login. The names are exactly
// the role output columns of bb:ceiling_role (pinned by
// TestCeilingRoleColumnsMatchGo); a missing or unreadable flag fails closed,
// never counted as 0. The SQL2022 ##MS_*## fixed roles need no separate flag:
// their rights are ordinary permission grants and the effective-permission
// proof below sees them (a ##MS_*## role carrying more than the ceiling makes
// perm_outside non-zero).
var ceilingRoles = []string{
	"sysadmin",
	"securityadmin",
	"serveradmin",
	"setupadmin",
	"processadmin",
	"dbcreator",
	"bulkadmin",
	"diskadmin",
}

// ceilingProofFlags are the positive-proof failure flags of bb:ceiling_role
// (W9-01). All are 0 on a readable, resolved, in-ceiling proof; any non-zero
// value refuses, and a missing/NULL flag is unreadable. perm_outside is an
// excess-right refusal; the other three mean the proof itself did not read.
var ceilingProofFlags = []string{
	"principal_missing",
	"perm_empty",
	"perm_outside",
	"connect_missing",
}

// CheckCeiling refuses any login with more rights than the Blackbox needs:
// membership in ANY of ceilingRoles, or any EFFECTIVE server permission
// outside ceilingAllowed (roles, groups, public and ##MS_*## included). The
// proof must be positively readable: an empty permission list, an unresolved
// principal or a missing CONNECT SQL fails closed. Only the two fixed control
// statements are sent, through the allowlisted Query path; every failure keeps
// its C8 origin via ceilingError. ctx is the caller's tick budget: the C4
// whole-tick limit has to cover the control queries too, otherwise a tick can
// spend 5 s on samples plus 6 s on rights checks and the "one tick, one
// budget" claim is false.
func CheckCeiling(ctx context.Context, q Queryer) error {
	roleText, ok := Statement("bb:ceiling_role")
	if !ok {
		return &ceilingError{errCeilingUnreadable}
	}
	rows, err := Query(ctx, q, roleText, ceilingTimeout)
	if err != nil {
		return ceilingFail(err) // Query already returns fixed-code errors
	}
	if len(rows) != 1 {
		return &ceilingError{errCeilingUnreadable}
	}
	// Every role flag must be present and readable: a missing column (nil) is
	// unreadable, so dropping a role from the query can never widen the ceiling.
	for _, role := range ceilingRoles {
		n, ok := ceilingFlag(rows[0][role])
		if !ok {
			return &ceilingError{errCeilingUnreadable}
		}
		if n != 0 {
			return &ceilingError{ErrTooManyRights}
		}
	}
	// The positive proof (W9-01): the effective-permission aggregates must be
	// readable and clean. An empty result is NOT a pass.
	for _, flag := range ceilingProofFlags {
		n, ok := ceilingFlag(rows[0][flag])
		if !ok {
			return &ceilingError{errCeilingUnreadable}
		}
		if n != 0 {
			if flag == "perm_outside" {
				return &ceilingError{ErrTooManyRights}
			}
			return &ceilingError{errCeilingUnreadable}
		}
	}
	permsText, ok := Statement("bb:ceiling_perms")
	if !ok {
		return &ceilingError{errCeilingUnreadable}
	}
	prows, err := Query(ctx, q, permsText, ceilingTimeout)
	if err != nil {
		return ceilingFail(err)
	}
	// bb:ceiling_perms returns ONLY effective permissions outside the ceiling
	// (its NOT IN filter keeps this fail-closed under the C6 row cap); the Go
	// subset assert is the second gate on every returned row.
	for _, r := range prows {
		name, ok := ceilingName(r["permission_name"])
		if !ok {
			return &ceilingError{errCeilingUnreadable}
		}
		if !ceilingAllowed[strings.ToUpper(strings.TrimSpace(name))] {
			return &ceilingError{ErrTooManyRights}
		}
	}
	return nil
}

// ceilingFlag reads a 0/1 role-membership flag. NULL or any non-numeric shape
// is unreadable (fail closed), never treated as 0.
func ceilingFlag(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	case []byte:
		i, err := strconv.ParseInt(strings.TrimSpace(string(n)), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// ceilingName reads a permission_name value; anything else is unreadable.
func ceilingName(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	}
	return "", false
}
