package calltrace

import (
	"math"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
)

// SourceInfo is an importer present in the case. Validated is true when every
// file of that importer has a validated time zone rule. From and To are the
// first and last event time its files cover; a zero value means unknown.
type SourceInfo struct {
	Name      string
	Validated bool
	From, To  time.Time
}

// windowSlack is the tolerance applied to both ends of the window.
const windowSlack = time.Second

// maxCallSpan is the longest call the trace draws. A longer span is a clock
// anomaly: the log cannot be trusted for it.
const maxCallSpan = 6 * time.Hour

// maxAuditMs bounds a number read from the audit record (about 31 years).
const maxAuditMs = 1e12

// finite reports whether v is a usable number from the audit record or an event.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v < maxAuditMs }

// known reports whether p holds a usable, non-negative number.
func known(p *float64) bool { return p != nil && finite(*p) && *p >= 0 }

// maxLabelLen caps an event label.
const maxLabelLen = 40

// LaneOf maps an importer name to a lane key by its prefix.
func LaneOf(source string) string {
	switch {
	case strings.HasPrefix(source, "sql-"):
		return LaneSQL
	case strings.HasPrefix(source, "mongo-"), strings.HasPrefix(source, "mongod-"), strings.HasPrefix(source, "mongostat-"):
		return LaneMongo
	case strings.HasPrefix(source, "redis-"):
		return LaneRedis
	case strings.HasPrefix(source, "iis-"):
		return LaneIIS
	}
	return LaneOther
}

func copyMs(p *float64) *float64 {
	if p == nil || !finite(*p) {
		return nil
	}
	v := *p
	return &v
}

// yearOK reports whether t can be written as JSON and shifted by a margin.
func yearOK(t time.Time) bool {
	y := t.UTC().Year()
	return y >= 1 && y <= 9999
}

// CallFromRecord turns an audit record into a Call. ok is false when the record
// has no usable start (missing, or a year outside 1..9999) or no id. A finish
// before the start, outside the year range, or more than maxCallSpan after the
// start sets ClockAnomaly.
func CallFromRecord(r audit.RequestRecord) (Call, bool) {
	if r.InitiatedAt == nil || r.InitiatedAt.IsZero() || r.ID == "" || !yearOK(*r.InitiatedAt) {
		return Call{}, false
	}
	c := Call{
		ID:         r.ID,
		SourceID:   r.SourceID,
		Controller: r.Controller,
		Action:     r.Action,
		Outcome:    r.Outcome,
		Start:      r.InitiatedAt.UTC(),
		Parts:      []Part{},
	}
	if r.CompletedAt != nil {
		if r.CompletedAt.Before(c.Start) || !yearOK(*r.CompletedAt) || r.CompletedAt.Sub(c.Start) > maxCallSpan {
			c.ClockAnomaly = true
		} else {
			end := r.CompletedAt.UTC()
			c.End = &end
		}
	}
	if !c.ClockAnomaly {
		switch {
		case known(r.DurationMs):
			c.DurationMs = copyMs(r.DurationMs)
		case c.End != nil:
			d := float64(c.End.Sub(c.Start)) / float64(time.Millisecond)
			c.DurationMs = &d
		}
	}
	if known(r.AuditCreateTimeMs) {
		c.Parts = append(c.Parts, Part{Key: PartAudit, Label: "Audit write", Ms: *r.AuditCreateTimeMs})
	}
	if known(r.ApiTimeMs) {
		c.Parts = append(c.Parts, Part{Key: PartAPI, Label: "API work", Ms: *r.ApiTimeMs})
	}
	if c.DurationMs != nil && (known(r.AuditCreateTimeMs) || known(r.ApiTimeMs)) {
		other := *c.DurationMs
		if known(r.AuditCreateTimeMs) {
			other -= *r.AuditCreateTimeMs
		}
		if known(r.ApiTimeMs) {
			other -= *r.ApiTimeMs
		}
		if other > 0 {
			c.Parts = append(c.Parts, Part{Key: PartOther, Label: "Other time", Ms: other})
		}
	}
	if known(r.PayloadRecordTimeMs) {
		c.Parts = append(c.Parts, Part{Key: PartPayload, Label: "Payload record", Ms: *r.PayloadRecordTimeMs})
	}
	return c, true
}

// EventLabel returns a short plain phrase for an event. It never includes an
// attribute value other than the wait class word, and an unlisted kind is "event".
func EventLabel(e evidence.Event) string {
	var s string
	switch e.Kind {
	case "":
		s = "event"
	case "slow-op":
		s = "slow operation"
	case "slow-command":
		s = "slow command"
	case "inflight-op":
		s = "running operation"
	case "app-request":
		s = "slow request"
	case "heartbeat-late":
		s = "late heartbeat"
	case "proxy-command":
		s = "slow proxy command"
	case "process-gone":
		s = "process exit window"
	case "app-session":
		s = "session marker"
	case "wait":
		s = "wait"
		switch w := e.Attrs["wait_class"]; w {
		case "lock", "latch", "preemptive":
			s = w + " wait"
		}
	default:
		s = "event" // an unlisted kind name is never shown
	}
	if r := []rune(s); len(r) > maxLabelLen {
		s = string(r[:maxLabelLen])
	}
	return s
}

func laneLabel(key string) string {
	switch key {
	case LaneSQL:
		return "SQL Server"
	case LaneMongo:
		return "MongoDB"
	case LaneRedis:
		return "Redis"
	case LaneIIS:
		return "IIS"
	}
	return "Other sources"
}

func eventLenMs(e evidence.Event) float64 {
	if known(e.DurationMs) {
		return *e.DurationMs
	}
	return float64(e.End.Sub(e.Start)) / float64(time.Millisecond)
}

// severityOf keeps only the severities evidence.Event defines.
func severityOf(s string) string {
	switch s {
	case "info", "warning", "error", "critical":
		return s
	}
	return ""
}

// MarkIncomplete returns lanes where every none-seen lane says incomplete. Use
// it when the event read behind the lanes hit its cap: a lane with no event
// then proves nothing. Other states are kept. It does not change lanes.
func MarkIncomplete(lanes []Lane) []Lane {
	out := make([]Lane, len(lanes))
	copy(out, lanes)
	for i := range out {
		if out[i].State == StateNoneSeen {
			out[i].State = StateIncomplete
		}
	}
	return out
}

// BuildLanes returns the lanes for one window: sql, mongo, redis, iis, and the
// other lane only when a source maps to it. It does not change evs.
func BuildLanes(win Window, haveCase bool, sources []SourceInfo, evs []evidence.Event) []Lane {
	validated := map[string]bool{} // importer name -> validated
	cover := map[string]*coverage{}
	for _, s := range sources {
		v, seen := validated[s.Name]
		validated[s.Name] = (!seen || v) && s.Validated
		c := cover[s.Name]
		if c == nil {
			c = &coverage{known: true}
			cover[s.Name] = c
		}
		c.add(s)
	}
	names := map[string][]string{}
	unchecked := map[string]bool{}
	for name, v := range validated {
		k := LaneOf(name)
		names[k] = append(names[k], name)
		if !v {
			unchecked[k] = true
		}
	}
	keys := []string{LaneSQL, LaneMongo, LaneRedis, LaneIIS}
	if len(names[LaneOther]) > 0 {
		keys = append(keys, LaneOther)
	}

	from, to := win.From.Add(-windowSlack), win.To.Add(windowSlack)
	qualifying := map[string][]evidence.Event{}
	for _, e := range evs {
		if _, ok := validated[e.Source]; !ok || e.End.Before(e.Start) || e.End.Before(from) || e.Start.After(to) {
			continue
		}
		k := LaneOf(e.Source)
		qualifying[k] = append(qualifying[k], e)
	}

	// outside reports whether every source of lane k has a known span and none
	// of those spans reaches the window.
	outside := func(k string) bool {
		for _, n := range names[k] {
			c := cover[n]
			if !c.known || (!c.to.Before(from) && !c.from.After(to)) {
				return false
			}
		}
		return len(names[k]) > 0
	}

	lanes := make([]Lane, 0, len(keys))
	for _, k := range keys {
		l := Lane{Key: k, Label: laneLabel(k), Sources: append([]string{}, names[k]...), Events: []LaneEvent{}}
		sort.Strings(l.Sources)
		switch {
		case !haveCase:
			l.State = StateNoCase
		case len(l.Sources) == 0:
			l.State = StateNoData
		case unchecked[k]:
			l.State = StateClockUnchecked
		case len(qualifying[k]) == 0 && outside(k):
			l.State = StateOutOfRange
		case len(qualifying[k]) == 0:
			l.State = StateNoneSeen
		default:
			l.State = StateEvents
			l.Events, l.Hidden = laneEvents(qualifying[k])
		}
		lanes = append(lanes, l)
	}
	return lanes
}

// coverage is the span the files of one importer cover. known is false when
// any of its files has no span; then nothing is claimed about the window.
type coverage struct {
	from, to time.Time
	known    bool
}

func (c *coverage) add(s SourceInfo) {
	if s.From.IsZero() || s.To.IsZero() || s.To.Before(s.From) {
		c.known = false
		return
	}
	if c.from.IsZero() || s.From.Before(c.from) {
		c.from = s.From
	}
	if c.to.IsZero() || s.To.After(c.to) {
		c.to = s.To
	}
}

// laneEvents keeps the MaxLaneEvents longest events (ties: earlier start, then
// id), sorted by start then id. It returns how many were left out.
func laneEvents(in []evidence.Event) ([]LaneEvent, int) {
	evs := append([]evidence.Event(nil), in...)
	hidden := 0
	if len(evs) > MaxLaneEvents {
		sort.SliceStable(evs, func(i, j int) bool {
			a, b := eventLenMs(evs[i]), eventLenMs(evs[j])
			if a != b {
				return a > b
			}
			if !evs[i].Start.Equal(evs[j].Start) {
				return evs[i].Start.Before(evs[j].Start)
			}
			return evs[i].EvidenceID < evs[j].EvidenceID
		})
		hidden = len(evs) - MaxLaneEvents
		evs = evs[:MaxLaneEvents]
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if !evs[i].Start.Equal(evs[j].Start) {
			return evs[i].Start.Before(evs[j].Start)
		}
		return evs[i].EvidenceID < evs[j].EvidenceID
	})
	out := make([]LaneEvent, len(evs))
	for i, e := range evs {
		out[i] = LaneEvent{
			ID: e.EvidenceID, Source: e.Source, Label: EventLabel(e),
			Start: e.Start.UTC(), End: e.End.UTC(),
			DurationMs: copyMs(e.DurationMs), Severity: severityOf(e.Severity),
		}
	}
	return out, hidden
}
