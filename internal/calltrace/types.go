// Package calltrace builds the per-call timeline: one audited API call as lanes
// (SQL, Mongo, Redis, IIS, other) on one clock, plus the automatic checks that
// read those lanes. It states no cause. Every sentence is a fixed template and
// passes evidence.LintCausal. Pure functions only: the server reads the audit
// record and the case events and hands them in.
package calltrace

import "time"

// Lane keys, in display order.
const (
	LaneSQL   = "sql"
	LaneMongo = "mongo"
	LaneRedis = "redis"
	LaneIIS   = "iis"
	LaneOther = "other"
)

// Lane states. The user must be able to tell "no data" from "nothing happened".
const (
	StateEvents         = "events"          // at least one event overlaps the window
	StateNoneSeen       = "none-seen"       // files exist, no qualifying event in the window
	StateNoData         = "no-data"         // the case holds no file for this lane
	StateClockUnchecked = "clock-unchecked" // files exist but a time zone rule is not validated; nothing plotted
	StateNoCase         = "no-case"         // no case is open, so there are no files at all
	StateIncomplete     = "incomplete"      // the read for this call hit its limit, so events may be missing; nothing is claimed
	StateOutOfRange     = "out-of-range"    // the files of this lane end or start away from the window; nothing plotted
)

// Check statuses (the four results of the theory-engine design, section 3.1).
const (
	StatusConsistent    = "consistent"      // "Consistent with this idea"
	StatusContradicted  = "contradicted"    // "The data does not fit this idea"
	StatusNotEnoughData = "not-enough-data" // "Not enough data"
	StatusNotTestable   = "not-testable"    // "Not testable here" (Needs says what is missing)
)

// Part keys of the time split of one call.
const (
	PartAudit   = "audit"   // audit create time
	PartAPI     = "api"     // API processing time
	PartPayload = "payload" // payload record time (measured after the work; not inside the total)
	PartOther   = "other"   // total minus audit minus api, when positive
)

// Part is one slice of a call's time.
type Part struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Ms    float64 `json:"ms"`
}

// Call is one audited API call as the trace sees it. Start is UTC. End is nil
// when the log has no usable completion (never finished, or completed before it
// started, which sets ClockAnomaly).
type Call struct {
	ID           string     `json:"id"`
	SourceID     string     `json:"sourceId"`
	Controller   string     `json:"controller"`
	Action       string     `json:"action"`
	Outcome      string     `json:"outcome"`
	Start        time.Time  `json:"start"`
	End          *time.Time `json:"end"`
	DurationMs   *float64   `json:"durationMs"`
	ClockAnomaly bool       `json:"clockAnomaly"`
	Parts        []Part     `json:"parts"` // audit, api, other (in that order); payload last when known
}

// LaneEvent is one imported event worth plotting. No attributes and no file
// names leave the server; Label is a plain phrase.
type LaneEvent struct {
	ID         string    `json:"id"`     // evidence id
	Source     string    `json:"source"` // importer name
	Label      string    `json:"label"`  // plain phrase, e.g. "lock wait"
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs *float64  `json:"durationMs,omitempty"`
	Severity   string    `json:"severity,omitempty"`
}

// Lane is one row of the trace.
type Lane struct {
	Key     string      `json:"key"`
	Label   string      `json:"label"`
	State   string      `json:"state"`
	Sources []string    `json:"sources"` // importer names present in the case for this lane
	Events  []LaneEvent `json:"events"`  // at most MaxLaneEvents, longest first, then sorted by Start
	Hidden  int         `json:"hidden"`  // qualifying events beyond MaxLaneEvents
}

// MaxLaneEvents bounds the events of one lane in one trace.
const MaxLaneEvents = 60

// Check is one automatic check. Detail is a fixed template. Needs names the
// missing source and is set only for StatusNotTestable. Label is Symptom or
// Correlation; v1 emits Symptom only.
type Check struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Detail  string   `json:"detail"`
	Needs   string   `json:"needs,omitempty"`
	CallIDs []string `json:"callIds"`
	Label   string   `json:"label"`
}

// Trace is the answer for one call.
type Trace struct {
	Call   Call    `json:"call"`
	Window Window  `json:"window"`
	Lanes  []Lane  `json:"lanes"`
	Checks []Check `json:"checks"`
}

// Window is the time range the lanes were read for (UTC).
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Result is the answer for one request: one to three traces, plus the checks
// that compare them (empty for one call).
type Result struct {
	Traces       []Trace `json:"traces"`
	Compare      []Check `json:"compare"`
	SharedWindow *Window `json:"sharedWindow,omitempty"` // set when all calls fit one 2 hour window (display only; each call reads its own window)
	Truncated    bool    `json:"truncated"`              // the event read of at least one call hit its cap
}
