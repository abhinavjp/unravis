package bbsource

import (
	"encoding/json"
	"strconv"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/sqlserver"
	"api-audit-explorer/internal/importer/wincsv"
)

// The event log, SQL log and dump groups of a sample. Each is a list of counted, fixed-word entries the recorder wrote
// (internal/blackbox/events.go, sqllog.go, dumps.go): numbers and enums only. No message text, no provider name, no login,
// no host name, no path and no dump file name is read.

// Event kinds of these groups. The first four are the kinds the Windows event and SQL Server importers already use, so a
// lane that holds both reads the same; the last is new.
const (
	KindWinEvent = "win-event"
	KindDumpFile = "dump-file" // a memory dump file the recorder saw appear
)

// Index bases of the events of these groups on one line.
const (
	idxLogEvents = 1<<19 + 1<<16
	idxSQLErrors = 1<<19 + 1<<17
	idxSQLHealth = 1<<19 + 1<<17 + 1<<16
	idxDumps     = 1<<19 + 1<<18
)

const maxLogEntries = 256 // per group and sample

// errLoginFailed is SQL Server error 18456, which the ERRORLOG importer reads as a failed login.
const errLoginFailed = 18456

type winEventEntry struct {
	Channel uint8     `json:"channel"`
	EventID uint32    `json:"eventId"`
	Level   uint8     `json:"level"`
	Count   uint32    `json:"count"`
	T       time.Time `json:"t"`
}

type sqlLogEntry struct {
	Kind     string    `json:"kind"`
	Number   uint32    `json:"number"`
	Severity uint8     `json:"severity"`
	Count    uint32    `json:"count"`
	T        time.Time `json:"t"`
}

type sqlHealthEntry struct {
	Kind  string    `json:"kind"`
	Count uint32    `json:"count"`
	MaxMs uint64    `json:"maxMs"`
	T     time.Time `json:"t"`
}

type dumpEntry struct {
	Name  string    `json:"name"` // used only to tell one dump from another; never copied
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// decodeEach decodes a list entry by entry, so one damaged entry (a time that does not parse, a wrong type) costs only
// itself.
func decodeEach[T any](raw []json.RawMessage, nt *notes) []T {
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		var v T
		if json.Unmarshal(r, &v) != nil {
			nt.damaged = true
			continue
		}
		out = append(out, v)
	}
	return out
}

// logChannels is the recorder's fixed channel enum.
var logChannels = map[uint8]string{1: "System", 2: "Application"}

// winLevelSeverity is how the Windows event importer maps a Level.
func winLevelSeverity(l uint8) string {
	switch l {
	case 1:
		return "critical"
	case 2:
		return "error"
	case 3:
		return "warning"
	}
	return "info"
}

// sqlLogKinds maps the recorder's ERRORLOG kinds to the ERRORLOG importer's event kinds.
var sqlLogKinds = map[string]string{
	"error": sqlserver.KindError, "startup": sqlserver.KindStart, "shutdown": sqlserver.KindStop, "slowio": sqlserver.KindIOStall,
}

// sqlHealthKinds maps the recorder's system_health kinds to the system_health importer's event kinds.
var sqlHealthKinds = map[string]string{
	"deadlock": sqlserver.KindDeadlock, "longwait": sqlserver.KindWait, "nonyield": sqlserver.KindNonYielding,
	"slowio": sqlserver.KindIOStall, "error": sqlserver.KindError,
}

// logEvents makes the events of the event log, SQL log and dump groups of one sample. An entry that is not a valid reading
// is left out by itself.
func logEvents(l *line, t time.Time, no int64, opts importer.Options, host string, nt *notes) []evidence.Event {
	var evs []evidence.Event
	mk := func(kind string, idx int, at time.Time, attrs map[string]string) evidence.Event {
		e := wincsv.NewEvent(opts, Name, no, idx)
		e.Kind, e.Host, e.TZRule, e.Attrs = kind, host, clock.RuleUTCField, attrs
		e.Start, e.End = at, at
		return e
	}
	okTime := func(at time.Time) (time.Time, bool) {
		at = at.UTC()
		if at.IsZero() || !wincsv.SaneTime(at) {
			nt.damaged = true
			return at, false
		}
		return at, true
	}
	count := func(n uint32) *float64 { v := float64(n); return &v }

	if len(l.Events) > 0 {
		var g struct {
			Events     []json.RawMessage `json:"events"`
			Truncated  bool              `json:"truncated"`
			Unreadable uint32            `json:"unreadable"`
			Lost       uint64            `json:"lost"`
		}
		if json.Unmarshal(l.Events, &g) != nil {
			nt.damaged = true
		}
		nt.eventsSeen = true
		if g.Truncated || g.Unreadable > 0 || g.Lost > 0 {
			nt.recorderLoss = true
		}
		if len(g.Events) > maxLogEntries {
			g.Events, nt.cut = g.Events[:maxLogEntries], true
		}
		es := decodeEach[winEventEntry](g.Events, nt)
		for i, x := range es {
			log, known := logChannels[x.Channel]
			at, good := okTime(x.T)
			if !known || !good || x.Count == 0 {
				nt.damaged = true
				continue
			}
			e := mk(KindWinEvent, idxLogEvents+i, at, map[string]string{"log": log, "id": strconv.FormatUint(uint64(x.EventID), 10)})
			e.Severity, e.Value, e.Unit = winLevelSeverity(x.Level), count(x.Count), "count"
			evs = append(evs, e)
		}
	}
	if len(l.SQLLog) > 0 {
		var g struct {
			Errors            []json.RawMessage `json:"errors"`
			Health            []json.RawMessage `json:"health"`
			Truncated         bool              `json:"truncated"`
			Unreadable        uint32            `json:"unreadable"`
			Lost              uint64            `json:"lost"`
			HealthUnavailable bool              `json:"healthUnavailable"`
		}
		if json.Unmarshal(l.SQLLog, &g) != nil {
			nt.damaged = true
		}
		if len(g.Errors) > 0 || len(g.Health) > 0 {
			nt.sqlLog = true
		}
		if g.Truncated || g.Unreadable > 0 || g.Lost > 0 || g.HealthUnavailable {
			nt.recorderLoss = true
		}
		if len(g.Errors) > maxLogEntries {
			g.Errors, nt.cut = g.Errors[:maxLogEntries], true
		}
		if len(g.Health) > maxLogEntries {
			g.Health, nt.cut = g.Health[:maxLogEntries], true
		}
		es := decodeEach[sqlLogEntry](g.Errors, nt)
		for i, x := range es {
			kind, known := sqlLogKinds[x.Kind]
			at, good := okTime(x.T)
			if !known || !good || x.Count == 0 || x.Severity > 25 {
				nt.damaged = true
				continue
			}
			attrs := map[string]string{}
			sev := "info"
			switch kind {
			case sqlserver.KindError:
				if x.Number == errLoginFailed {
					// A failed login is its own kind in the ERRORLOG importer, a warning with no attributes.
					kind, sev = sqlserver.KindLoginFailed, "warning"
					break
				}
				attrs["error_number"], attrs["sql_severity"] = strconv.FormatUint(uint64(x.Number), 10), strconv.Itoa(int(x.Severity))
				sev = sqlserver.SeverityOf(int(x.Severity))
			case sqlserver.KindIOStall:
				sev = "warning"
			}
			e := mk(kind, idxSQLErrors+i, at, attrs)
			e.Severity, e.Value, e.Unit = sev, count(x.Count), "count"
			evs = append(evs, e)
		}
		hs := decodeEach[sqlHealthEntry](g.Health, nt)
		for i, x := range hs {
			kind, known := sqlHealthKinds[x.Kind]
			at, good := okTime(x.T)
			if !known || !good || x.Count == 0 {
				nt.damaged = true
				continue
			}
			e := mk(kind, idxSQLHealth+i, at, map[string]string{})
			e.Severity, e.Value, e.Unit = "warning", count(x.Count), "count"
			if kind == sqlserver.KindNonYielding {
				e.Severity = "error"
			}
			if x.MaxMs > 0 && x.MaxMs < 1<<40 {
				d := float64(x.MaxMs)
				e.DurationMs = &d
			}
			evs = append(evs, e)
		}
	}
	if len(l.Dumps) > 0 {
		var g struct {
			Dumps      []json.RawMessage `json:"dumps"`
			Truncated  bool              `json:"truncated"`
			Unreadable int               `json:"unreadable"`
		}
		if json.Unmarshal(l.Dumps, &g) != nil {
			nt.damaged = true
		}
		nt.dumpsSeen = true
		if g.Truncated || g.Unreadable > 0 {
			nt.recorderLoss = true
		}
		if len(g.Dumps) > maxLogEntries {
			g.Dumps, nt.cut = g.Dumps[:maxLogEntries], true
		}
		ds := decodeEach[dumpEntry](g.Dumps, nt)
		if nt.dumpSeen == nil {
			nt.dumpSeen = map[dumpKey]struct{}{}
		}
		for i, x := range ds {
			at, good := okTime(x.MTime)
			if !good || x.Size < 0 {
				nt.damaged = true
				continue
			}
			if at.Before(nt.firstT.Add(-beforeFileMargin)) {
				continue // it existed before this file began: the file before it holds the sight
			}
			key := dumpKey{h: fnv(x.Name)}
			if _, dup := nt.dumpSeen[key]; dup {
				continue
			}
			if len(nt.dumpSeen) < seenCap {
				nt.dumpSeen[key] = struct{}{}
			} else {
				nt.cut = true
			}
			v := float64(x.Size)
			e := mk(KindDumpFile, idxDumps+i, at, map[string]string{})
			e.Severity, e.Value, e.Unit = "warning", &v, "bytes"
			evs = append(evs, e)
		}
	}
	return evs
}

// dumpKey tells one dump file from another without keeping its name. The name alone decides: a dump that is still being
// written changes its size and time between two scans and is still one dump.
type dumpKey struct{ h uint64 }

// fnv is a 64-bit FNV-1a of s, kept in memory only to tell two dump files apart; the name itself is never kept.
func fnv(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
