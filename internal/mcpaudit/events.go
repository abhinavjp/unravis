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

// Kinds of an audit row written by the dispatcher.
const (
	KindAdmit  = "admit"  // the call passed the gate and is about to run
	KindEnd    = "end"    // the call ended (Outcome says how)
	KindDenied = "denied" // the call was refused before it ran, or its answer was withheld
)

// Outcomes of an audit row.
const (
	OutcomeOK        = "ok"
	OutcomeDenied    = "denied"
	OutcomeError     = "error"
	OutcomeCancelled = "cancelled"
	OutcomeTimeout   = "timeout"
)

// MaxEvents is the most rows kept; older rows are deleted as new ones come in (spec 03 D10: 100,000 events). The 90-day rule is BL-351's.
const MaxEvents = 100_000

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
	case KindAdmit, KindEnd, KindDenied:
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
		_, _ = d.sql.ExecContext(ctx, `DELETE FROM audit_events WHERE id <= (SELECT max(id) FROM audit_events) - ?`, MaxEvents)
	}
	return nil
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
	defer rows.Close()
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
