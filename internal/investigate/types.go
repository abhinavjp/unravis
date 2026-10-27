package investigate

import (
	"strings"
	"time"
)

// Tri is a three-valued signal. Unknown is never "no": a signal whose data is
// absent stays unknown, and an unknown signal can make a suspect "Cannot test yet"
// or "Partly consistent", but never "Contradicted".
type Tri string

const (
	Yes     Tri = "yes"
	No      Tri = "no"
	Unknown Tri = "unknown"
)

// Readiness is the fixed status word of a step (decision A5). There is no
// "Pass": a step with missing data says "Cannot test yet".
type Readiness string

const (
	Ready         Readiness = "Ready"
	PartlyReady   Readiness = "Partly ready"
	CannotTestYet Readiness = "Cannot test yet"
	Failed        Readiness = "Failed"
)

// Window is a half-open UTC interval [Start, End).
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Valid reports whether the window has a start and a later end.
func (w Window) Valid() bool { return !w.Start.IsZero() && w.End.After(w.Start) }

// Contains reports whether t falls inside [Start, End).
func (w Window) Contains(t time.Time) bool { return !t.Before(w.Start) && t.Before(w.End) }

// Overlaps reports whether the two windows share any time.
func (w Window) Overlaps(o Window) bool { return w.Start.Before(o.End) && o.Start.Before(w.End) }

// minutes is the number of whole minute buckets the window touches.
func (w Window) minutes() int {
	if !w.Valid() {
		return 0
	}
	first := minuteOf(w.Start)
	last := minuteOf(w.End.Add(-time.Nanosecond))
	return int(last.Sub(first)/time.Minute) + 1
}

// AuditStatus says whether the audit source could be read for the case.
type AuditStatus string

const (
	AuditPresent AuditStatus = "present"
	AuditMissing AuditStatus = "missing"
	AuditFailed  AuditStatus = "failed"
)

// Request is one audited call, reduced to what the engine reads. The conversion
// from an audit record is FromRecords; everything else here is plain data.
type Request struct {
	ID    string `json:"id"`
	Route string `json:"route"` // "Controller.Action"
	// Start and End are the audit RequestInitiatedAt and RequestCompletedAt. End is
	// nil for a call that has not completed. D is End minus Start (A21).
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"`
	// A, C and P are the audit parts in milliseconds (A21): A is audit set-up plus
	// the synchronous Mongo insert, C is the rest of the filter chain plus the
	// action, P is payload capture after D. Nil means the document lacks the field.
	A *float64 `json:"a,omitempty"`
	C *float64 `json:"c,omitempty"`
	P *float64 `json:"p,omitempty"`
	// Client is the user id or address that made the call; empty when unknown.
	Client string `json:"client,omitempty"`
	// Path is the request path; it is how a call is matched to an IIS row.
	Path string `json:"path,omitempty"`
	// Outcome is the audit status class ("success", "client_error",
	// "server_error", ...); empty when the document carries none.
	Outcome string `json:"outcome,omitempty"`
}

// IISRow is one IIS log row, reduced to what the engine reads. Start and End are
// the request start and end as the importer derives them; TakenMs is time-taken.
type IISRow struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	TakenMs float64   `json:"takenMs"`
	Path    string    `json:"path"`
}

// Release is one change the user typed into the change list (Step 5).
type Release struct {
	At    time.Time `json:"at"`
	Label string    `json:"label"`
}

// Input is everything one run reads. Nothing in it is fetched by the engine, so
// the same Input always gives the same Result.
type Input struct {
	Incident  Window `json:"incident"`
	Reference Window `json:"reference"`
	// Route, when set ("Controller.Action"), narrows Steps 2 to 4 to one API. Scope
	// still reads every route.
	Route string `json:"route,omitempty"`

	Audit    AuditStatus `json:"audit"`
	Requests []Request   `json:"requests"`
	// NoStart counts audit documents the reader had to drop because they carry no
	// start time. They are reported, never counted as calls.
	NoStart int `json:"noStart,omitempty"`

	// IISPresent is true when an IIS source is in the case; IIS is its rows. An IIS
	// source with no rows in the windows is present and empty, which is not the same
	// as no IIS source.
	IISPresent bool     `json:"iisPresent"`
	IIS        []IISRow `json:"iis,omitempty"`

	// Releases is the change list; ReleasesEntered is true once the user has
	// entered it (an empty list entered on purpose reads "no change listed").
	Releases        []Release `json:"releases,omitempty"`
	ReleasesEntered bool      `json:"releasesEntered"`

	// PoolLimit is the SqlClient pool limit the user entered; nil means none entered.
	PoolLimit *int `json:"poolLimit,omitempty"`

	// Lanes and LaneOnsets are the seam for the SQL, Mongo and host lanes (later
	// phases). Lanes maps a catalog id to what that lane's own data says about it:
	// yes (its signature is present), no (its contradicting evidence is present),
	// unknown or absent (not tested). LaneOnsets maps a tier ("sql", "mongo",
	// "host") to when its own work metric first moved. LaneSources names the
	// sources those lanes read, for the "how sure are we" panel.
	Lanes       map[string]Tri       `json:"lanes,omitempty"`
	LaneOnsets  map[string]time.Time `json:"laneOnsets,omitempty"`
	LaneSources []string             `json:"laneSources,omitempty"`
	// LaneGaps maps a tier to the stretches its data has no readings for inside the
	// windows (rule A35). A short gap warns; a long one cannot be tested. Only the
	// host lane fills it in this release (see ApplyHost).
	LaneGaps map[string][]Span `json:"laneGaps,omitempty"`
}

// NormalizePath lower-cases a request path, drops a query string and a trailing
// slash, so an audit URL and an IIS stem compare equal.
func NormalizePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(p, "://"); i >= 0 {
		rest := p[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			p = rest[j:]
		} else {
			p = "/"
		}
	}
	p = strings.ToLower(strings.TrimSpace(p))
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

func minuteOf(t time.Time) time.Time { return t.UTC().Truncate(time.Minute) }
