package mcpaudit

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// The call ledger rows of BL-344 (spec 03 section 3.4). A row says who called what and how it ended; it never holds the arguments, a
// result, a path or a credential: the arguments are kept only as a keyed digest (Fingerprint), so two equal calls can be recognised and
// nothing can be read back. BL-351 adds the screen, the query and the 90-day prune; here only the writes and a hard row cap.

// Kinds of an audit row. The first three are written by the dispatcher for a call.
const (
	KindAdmit  = "admit"  // the call passed the gate and is about to run
	KindEnd    = "end"    // the call ended (Outcome says how)
	KindDenied = "denied" // the call was refused before it ran, or its answer was withheld

	// The lifecycle rows of a profile (BL-351), written by the grants: who was enrolled, approved, given a different scope, and revoked or
	// reissued. They carry the profile id, its revision and a short non-secret fact in Code, never a credential, digest, nonce or path.
	KindEnroll      = "enroll"       // a host was added or seen for the first time, or a second process contested it
	KindApprove     = "approve"      // the person approved a pending host
	KindScopeChange = "scope_change" // an approval gave a host a different scope than it had before
	KindRevoke      = "revoke"       // the host's credential ended (revoke or reissue)
)

// Outcomes of an audit row.
const (
	OutcomeOK        = "ok"
	OutcomeDenied    = "denied"
	OutcomeError     = "error"
	OutcomeCancelled = "cancelled"
	OutcomeTimeout   = "timeout"
)

// MaxEvents is the most rows kept; older rows are deleted as new ones come in (spec 03 D10: 100,000 events). RetainFor is how long a row
// is kept: the ledger screen says "kept 90 days".
const MaxEvents = 100_000

const RetainFor = 90 * 24 * time.Hour

const (
	pruneEvery    = 1000 // rows between two cap checks
	maxEventField = 64   // strings are cut here (spec 03 section 3.4)
)

// Event is one ledger row. Every string is cut at 64 bytes on write.
type Event struct {
	Time        time.Time
	ProfileID   string
	ProfileRev  int
	Kind        string
	Tool        string
	OpClass     string
	CorrID      string // the call id the bridge made up, random: it correlates admit and end
	DurMs       int64
	Outcome     string
	Code        string
	Bytes       int
	Fingerprint []byte // HMAC of the arguments (Fingerprint), never the arguments
}

// ErrBadEvent is a row with an unknown kind or no profile.
var ErrBadEvent = errors.New("mcpaudit: not a ledger row")

var inserted atomic.Uint64

// Fingerprint is the keyed digest of the arguments of a call. It is a one-way function under the server key, so a copy of the file
// cannot be used to test a guess of the arguments.
func (d *DB) Fingerprint(args []byte) []byte { return d.MAC("args", args) }

// Record writes one ledger row. It is one statement; the row is on disk when it returns (synchronous=FULL).
func (d *DB) Record(ctx context.Context, e Event) error {
	switch e.Kind {
	case KindAdmit, KindEnd, KindDenied, KindEnroll, KindApprove, KindScopeChange, KindRevoke:
	default:
		return ErrBadEvent
	}
	if e.ProfileID == "" {
		return ErrBadEvent
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO audit_events(ts,profile_id,profile_rev,kind,tool,op_class,corr_id,dur_ms,outcome,code,bytes,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		ms(e.Time), cut(e.ProfileID), e.ProfileRev, e.Kind, cut(e.Tool), cut(e.OpClass), cut(e.CorrID), e.DurMs, cut(e.Outcome), cut(e.Code), e.Bytes, e.Fingerprint); err != nil {
		return errors.New("mcpaudit: the ledger row was not written")
	}
	if inserted.Add(1)%pruneEvery == 0 {
		// A failed prune is not a failed call: the cap is checked again after the next thousand rows.
		_, _ = d.Prune(ctx, e.Time)
	}
	return nil
}

// Prune deletes the rows older than RetainFor (at now) and the rows beyond the newest MaxEvents, and returns how many it deleted. The
// owner calls it when the store is opened; Record calls it every thousand rows.
func (d *DB) Prune(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for _, q := range []struct {
		sql string
		arg any
	}{
		{`DELETE FROM audit_events WHERE ts < ?`, ms(now.Add(-RetainFor))},
		{`DELETE FROM audit_events WHERE id <= (SELECT max(id) FROM audit_events) - ?`, MaxEvents},
	} {
		r, err := d.sql.ExecContext(ctx, q.sql, q.arg)
		if err != nil {
			return total, errors.New("mcpaudit: the ledger could not be pruned")
		}
		n, _ := r.RowsAffected()
		total += n
	}
	// The evidence aliases that ended or whose profile is gone or changed (BL-349) go with the ledger rows.
	n, err := d.pruneAliases(ctx, now)
	total += n
	if err != nil {
		return total, err
	}
	return total, nil
}

// CountEvents returns how many ledger rows there are.
func (d *DB) CountEvents(ctx context.Context) (n int, err error) {
	err = d.sql.QueryRowContext(ctx, `SELECT count(*) FROM audit_events`).Scan(&n)
	return n, err
}

// cut shortens s to maxEventField bytes without cutting a character.
func cut(s string) string {
	if len(s) <= maxEventField {
		return s
	}
	s = s[:maxEventField]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// RecentEvents returns up to limit ledger rows, newest first. BL-351 builds the paged query of the screen on top of this; the rows have
// no free text, so there is nothing to scrub.
func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT ts,profile_id,profile_rev,kind,tool,op_class,corr_id,dur_ms,outcome,code,bytes,fingerprint FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, errors.New("mcpaudit: the ledger could not be read")
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var t int64
		if err := rows.Scan(&t, &e.ProfileID, &e.ProfileRev, &e.Kind, &e.Tool, &e.OpClass, &e.CorrID, &e.DurMs, &e.Outcome, &e.Code, &e.Bytes, &e.Fingerprint); err != nil {
			return nil, errors.New("mcpaudit: the ledger could not be read")
		}
		e.Time = tm(t)
		out = append(out, e)
	}
	return out, rows.Err()
}
