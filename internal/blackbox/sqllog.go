package blackbox

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// SQL Server ERRORLOG and system_health samplers (BL-551, "collect everything", P1b).
//
// The Blackbox HOST recorder (LocalSystem) opens the SQL Server files on disk. It
// needs no SQL login and no SQL right: the plan lists both as "rights: none", and a
// file that cannot be read shows as "not available" (HealthUnavailable, Unreadable).
// It never falls back to a SQL right.
//
// What is kept (security rule, reviewed with Opus because the ERRORLOG holds text
// that can name logins, hosts, databases, file paths and even statements):
//   - Fixed words and numbers only. Every field of SQLLogEvent is a number or one
//     of four fixed kinds. The text of a line is matched against a few fixed
//     patterns and only DIGITS are copied out of it. No line, no message, no
//     login, host or database name, no file path and no statement ever reaches a
//     sample, so there is nothing for a scrubber to miss.
//   - Bounded work: at most sqlLogPollBytes read per instance per poll, at most
//     sqlLogMaxInstances instances, at most sqlLogMaxEvents entries per sample.
//   - Regular files only: a link, junction or device is refused. The file is
//     opened read only and never written, moved or deleted.
//
// ERRORLOG is read forward from a cursor (file offset plus the first bytes of the
// file, to notice a cycle). The cursor moves only after the recorder has durably
// written the sample (Ack), the same rule as the Windows event log group. With no
// cursor the read starts at the END of the file: no backfill, like the events group.

func init() { RegisterGroup("sqlLog", true, newSQLLogGroup) }

// Limits.
const (
	sqlLogPollEvery     = 60 * time.Second // ERRORLOG is rare text; one look a minute is plenty
	sqlLogDiscoverEvery = 10 * time.Minute // instances change rarely
	sqlLogPollBytes     = 256 << 10        // read at most this much of one ERRORLOG per poll
	sqlLogMaxInstances  = 8
	sqlLogMaxEvents     = 256
	sqlLogHeadBytes     = 32
)

// SQL host log item ids, as they appear in sql.extras (they are not SQL samplers).
const (
	SQLLogItemErrlog    = "errlog"
	SQLLogItemSysHealth = "syshealth"
)

var sqlInstanceLabelRe = regexp.MustCompile(`^[A-Za-z0-9_$#@]{1,32}$`)

// sqlLogInstance is one SQL Server instance whose files the host may read.
type sqlLogInstance struct {
	Label   string // MSSQLSERVER or the instance name
	LogPath string // absolute path of its ERRORLOG
}

// errQuiet is "nothing to report this tick". The collector treats errNotApplicable
// as "leave no trace", which is right here: a poll every 60 s means most ticks have
// nothing new, and each of those must not read as a missing group in the sample.
var errQuiet = errNotApplicable

// errXELUnsupported says the system_health event file reader is not available.
var errXELUnsupported = errors.New("system_health files not readable")

// rawHealth is one decoded system_health event: a fixed kind, a time and (for a
// wait or an I/O stall) its duration. Never event text.
type rawHealth struct {
	Kind string
	T    time.Time
	Ms   uint64
}

// sqlLogCursor is the read position of one ERRORLOG.
type sqlLogCursor struct {
	Seen    bool // the file was looked at once (the first look only sets the position)
	Offset  int64
	Head    [sqlLogHeadBytes]byte
	HeadLen int
}

// sqlLogCursors is the whole read position of the group.
type sqlLogCursors struct {
	Log         map[string]sqlLogCursor
	HealthT     map[string]time.Time
	HealthNoted bool
}

func (c sqlLogCursors) clone() sqlLogCursors {
	out := sqlLogCursors{HealthNoted: c.HealthNoted, Log: map[string]sqlLogCursor{}, HealthT: map[string]time.Time{}}
	for k, v := range c.Log {
		out.Log[k] = v
	}
	for k, v := range c.HealthT {
		out.HealthT[k] = v
	}
	return out
}

func (c sqlLogCursors) equal(o sqlLogCursors) bool {
	return c.HealthNoted == o.HealthNoted && reflect.DeepEqual(c.Log, o.Log) && reflect.DeepEqual(c.HealthT, o.HealthT)
}

// sqlLogCommitted holds the acknowledged cursors (written by the recorder goroutine
// in Ack, read by the group's worker).
type sqlLogCommitted struct{ v atomic.Pointer[sqlLogCursors] }

func (s *sqlLogCommitted) load() sqlLogCursors {
	if p := s.v.Load(); p != nil {
		return p.clone()
	}
	return sqlLogCursors{}
}

func (s *sqlLogCommitted) store(c sqlLogCursors) { cc := c.clone(); s.v.Store(&cc) }

// Ack publishes this batch's read position. The recorder calls it only after the
// sample is durably written. Safe on nil and on a batch read back from disk.
func (g *SQLLogGroup) Ack() {
	if g == nil || g.commit == nil {
		return
	}
	g.commit.store(g.pending)
}

// SQLHostLogs says which of the two file samplers run: the "everything" and "deep"
// profiles turn both on, and sql.extras can add either one by id.
func (c Config) SQLHostLogs() (errlog, health bool) {
	switch c.SQLProfileName() {
	case SQLProfileEverything, SQLProfileDeep:
		return true, true
	}
	if c.SQL != nil {
		for _, x := range c.SQL.Extras {
			switch x {
			case SQLLogItemErrlog:
				errlog = true
			case SQLLogItemSysHealth:
				health = true
			}
		}
	}
	return errlog, health
}

type sqlLogPending struct {
	cursors sqlLogCursors
	group   SQLLogGroup // payload without the ack fields
}

type sqlLogGroup struct {
	errlog, health bool
	discover       func() []sqlLogInstance
	loc            *time.Location
	// decodeHealth reads the system_health files of one instance for events after
	// since. The real one is decodeSystemHealth.
	decodeHealth func(dir string, since time.Time) ([]rawHealth, error)

	committed sqlLogCommitted
	pending   *sqlLogPending // worker-only: batch waiting for its ack

	started      time.Time
	lastPoll     time.Time
	lastDiscover time.Time
	insts        []sqlLogInstance
}

func newSQLLogGroup(d GroupDeps) (Group, error) {
	errlog, health := d.Config.SQLHostLogs()
	if !errlog && !health {
		return nil, errNoTargets // Standard profile: nothing is read
	}
	if sqlLogInstancesPlatform == nil {
		return nil, errNotApplicable
	}
	return &sqlLogGroup{
		errlog: errlog, health: health,
		discover: sqlLogInstancesPlatform, loc: time.Local,
		decodeHealth: decodeSystemHealth,
	}, nil
}

func (g *sqlLogGroup) Name() string   { return "sqlLog" }
func (g *sqlLogGroup) Optional() bool { return true }

func (g *sqlLogGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.started.IsZero() {
		g.started = now // no backfill for system_health either
	}
	cur := g.committed.load()
	if g.pending != nil && g.pending.cursors.equal(cur) {
		g.pending = nil // acknowledged
	}
	if g.pending != nil {
		return g.payload(g.pending), nil // never skipped, re-sent until written
	}
	if !g.lastPoll.IsZero() && now.Sub(g.lastPoll) < sqlLogPollEvery && !now.Before(g.lastPoll) {
		return nil, errQuiet
	}
	g.lastPoll = now
	if g.lastDiscover.IsZero() || now.Sub(g.lastDiscover) >= sqlLogDiscoverEvery || now.Before(g.lastDiscover) {
		g.insts = g.found()
		g.lastDiscover = now
	}
	if len(g.insts) == 0 {
		return nil, errQuiet // no SQL Server on this host: stays quiet, looks again later
	}

	next := cur.clone()
	out := SQLLogGroup{}
	var errRows []SQLLogEvent
	var healthRows []SQLHealthEvent
	for _, in := range g.insts {
		if g.errlog {
			r := readErrorLog(in.LogPath, cur.Log[in.Label], sqlLogPollBytes, g.loc)
			next.Log[in.Label] = r.cur
			out.Truncated = out.Truncated || r.truncated
			out.Lost += r.lost
			if r.unreadable {
				out.Unreadable++
			}
			for _, e := range r.events {
				e.Instance = in.Label
				errRows = append(errRows, e)
			}
		}
		if g.health {
			since := cur.HealthT[in.Label]
			if since.IsZero() {
				since = g.started
			}
			evs, err := g.decodeHealth(filepath.Dir(in.LogPath), since)
			switch {
			case err != nil:
				out.HealthUnavailable = !next.HealthNoted
				next.HealthNoted = true
			default:
				latest := since
				for _, e := range evs {
					if !e.T.After(since) {
						continue
					}
					healthRows = append(healthRows, SQLHealthEvent{Instance: in.Label, Kind: e.Kind, Count: 1, MaxMs: e.Ms, T: e.T.UTC()})
					if e.T.After(latest) {
						latest = e.T
					}
				}
				next.HealthT[in.Label] = latest
			}
		}
	}
	out.Errors = aggregateSQLLog(errRows)
	out.Health = aggregateSQLHealth(healthRows)
	if len(out.Errors) == 0 && len(out.Health) == 0 && !out.Truncated && out.Unreadable == 0 && out.Lost == 0 && !out.HealthUnavailable {
		g.committed.store(next) // nothing to write: the position can move now
		return nil, errQuiet
	}
	g.pending = &sqlLogPending{cursors: next, group: out}
	return g.payload(g.pending), nil
}

func (g *sqlLogGroup) found() []sqlLogInstance {
	all := g.discover()
	var out []sqlLogInstance
	seen := map[string]bool{}
	for _, in := range all {
		if !sqlInstanceLabelRe.MatchString(in.Label) || seen[in.Label] || !sqlLogPathOK(in.LogPath) {
			continue
		}
		seen[in.Label] = true
		out = append(out, in)
		if len(out) == sqlLogMaxInstances {
			break
		}
	}
	return out
}

func (g *sqlLogGroup) payload(p *sqlLogPending) *SQLLogGroup {
	o := p.group
	o.commit = &g.committed
	o.pending = p.cursors
	return &o
}

// sqlLogPathOK says a registry path is worth opening: absolute, and the file is
// named ERRORLOG. The registry is written by administrators only, but the host
// reads as LocalSystem, so it still refuses any other name.
func sqlLogPathOK(p string) bool {
	return p != "" && filepath.IsAbs(p) && strings.EqualFold(filepath.Base(p), "ERRORLOG")
}

// aggregateSQLLog counts rows by every field but time, keeps the latest time and
// caps the entries (newest first).
func aggregateSQLLog(rows []SQLLogEvent) []SQLLogEvent {
	type key struct {
		inst, kind string
		num        uint32
		sev, st    uint8
		db, secs   uint32
	}
	by := map[key]*SQLLogEvent{}
	for _, r := range rows {
		k := key{r.Instance, r.Kind, r.Number, r.Severity, r.State, r.Database, r.Seconds}
		e := by[k]
		if e == nil {
			c := r
			c.Count = 0
			e = &c
			by[k] = e
		}
		if e.Count+r.Count < e.Count {
			e.Count = ^uint32(0)
		} else {
			e.Count += r.Count
		}
		if r.T.After(e.T) {
			e.T = r.T
		}
	}
	out := make([]SQLLogEvent, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].T.Equal(out[j].T) {
			return out[i].T.After(out[j].T)
		}
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Number < out[j].Number
	})
	if len(out) > sqlLogMaxEvents {
		out = out[:sqlLogMaxEvents]
	}
	return out
}

func aggregateSQLHealth(rows []SQLHealthEvent) []SQLHealthEvent {
	type key struct{ inst, kind string }
	by := map[key]*SQLHealthEvent{}
	for _, r := range rows {
		k := key{r.Instance, r.Kind}
		e := by[k]
		if e == nil {
			c := r
			c.Count = 0
			e = &c
			by[k] = e
		}
		e.Count += r.Count
		if r.MaxMs > e.MaxMs {
			e.MaxMs = r.MaxMs
		}
		if r.T.After(e.T) {
			e.T = r.T
		}
	}
	out := make([]SQLHealthEvent, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].T.Equal(out[j].T) {
			return out[i].T.After(out[j].T)
		}
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		return out[i].Kind < out[j].Kind
	})
	if len(out) > sqlLogMaxEvents {
		out = out[:sqlLogMaxEvents]
	}
	return out
}
