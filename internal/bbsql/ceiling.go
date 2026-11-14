// ceiling.go is the C8 rights ceiling: the runner calls CheckCeiling at EVERY
// (re)connect before any sampler runs. Two fixed, allowlisted control queries
// prove the login is in no privileged fixed server role and holds no server
// permission outside {CONNECT SQL, VIEW ANY DATABASE, VIEW SERVER STATE, VIEW
// SERVER PERFORMANCE STATE}. Both queries are SERVER-scoped: role membership
// via IS_SRVROLEMEMBER and permissions via SUSER_ID() (the login's server
// principal id), never a database principal id. Read-only: the app never grants
// or alters rights. Refusal is the fixed ErrTooManyRights. No goroutines, no
// tickers.
package bbsql

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// ceilingTimeout is the per-control-query budget (two small fixed queries).
const ceilingTimeout = 3 * time.Second

// ErrTooManyRights is the fixed C8 refusal (code too-many-rights).
var ErrTooManyRights = &Error{CodeTooManyRights, "this login has more rights than the Blackbox needs"}

// errCeilingUnreadable fails closed when a control result cannot be read.
var errCeilingUnreadable = &Error{CodeFailed, "The login rights check could not be completed."}

// ceilingAllowed is the complete C8 permission ceiling.
var ceilingAllowed = map[string]bool{
	"CONNECT SQL":                   true,
	"VIEW ANY DATABASE":             true,
	"VIEW SERVER STATE":             true,
	"VIEW SERVER PERFORMANCE STATE": true,
}

// ceilingRoles are the fixed server roles whose membership alone is more than
// the Blackbox needs, so ANY flag of 1 refuses the login. The names are exactly
// the output columns of bb:ceiling_role (pinned by TestCeilingRoleColumnsMatchGo);
// a missing or unreadable flag fails closed, never counted as 0.
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

// CheckCeiling refuses any login with more rights than the Blackbox needs:
// membership in ANY of ceilingRoles, or any server permission outside
// ceilingAllowed. Only the two fixed control statements are sent, through the
// allowlisted Query path; any failure or unreadable result fails closed.
func CheckCeiling(q Queryer) error {
	ctx := context.Background()
	roleText, ok := Statement("bb:ceiling_role")
	if !ok {
		return errCeilingUnreadable
	}
	rows, err := Query(ctx, q, roleText, ceilingTimeout)
	if err != nil {
		return err // Query already returns fixed-code errors
	}
	if len(rows) != 1 {
		return errCeilingUnreadable
	}
	// Every role flag must be present and readable: a missing column (nil) is
	// unreadable, so dropping a role from the query can never widen the ceiling.
	for _, role := range ceilingRoles {
		n, ok := ceilingFlag(rows[0][role])
		if !ok {
			return errCeilingUnreadable
		}
		if n != 0 {
			return ErrTooManyRights
		}
	}
	permsText, ok := Statement("bb:ceiling_perms")
	if !ok {
		return errCeilingUnreadable
	}
	prows, err := Query(ctx, q, permsText, ceilingTimeout)
	if err != nil {
		return err
	}
	// bb:ceiling_perms returns ONLY permissions outside the ceiling (its NOT
	// IN filter keeps this fail-closed under the C6 row cap); the Go subset
	// assert is the second gate on every returned row.
	for _, r := range prows {
		name, ok := ceilingName(r["permission_name"])
		if !ok {
			return errCeilingUnreadable
		}
		if !ceilingAllowed[strings.ToUpper(strings.TrimSpace(name))] {
			return ErrTooManyRights
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
