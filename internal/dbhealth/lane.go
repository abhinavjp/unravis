package dbhealth

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// The lanes read one database for one incident. They answer the investigation's questions for that database (which catalog entries its own
// data supports, which it contradicts, and when its own work first moved), never a verdict. A check that cannot run says "Cannot test yet" and
// shows the lines the owner runs to fix it; its entries stay unknown and are never reported as ruled out (decision A6).

// States use the fixed readiness words (decision A5).
const (
	StateReady  = "Ready"
	StateNotYet = "Cannot test yet"
	StateFailed = "Failed"
)

// Answer values. They match the investigation engine's yes and no; an entry a lane could not settle is simply absent.
const (
	AnswerYes = "yes"
	AnswerNo  = "no"
)

// Window is a span of UTC time, start inclusive and end exclusive.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (w Window) Valid() bool { return !w.Start.IsZero() && w.End.After(w.Start) }

func (w Window) Overlaps(o Window) bool { return w.Start.Before(o.End) && o.Start.Before(w.End) }

func (w Window) Same(o Window) bool { return w.Start.Equal(o.Start) && w.End.Equal(o.End) }

func (w Window) Contains(t time.Time) bool { return !t.Before(w.Start) && t.Before(w.End) }

// LaneRequest is what a lane run needs. Database is the one the checks are scoped to; Collection (Mongo) narrows the audit namespace. Login is
// the sign-in name, used only to fill the fix scripts.
type LaneRequest struct {
	Incident   Window
	Reference  Window
	Database   string
	Collection string
	Login      string
}

// Validate refuses a request the checks cannot compare: both windows must be real and the reference must end before the incident starts.
func (r LaneRequest) Validate() error {
	if !r.Incident.Valid() || !r.Reference.Valid() {
		return errors.New("both the incident window and the reference window need a start and an end")
	}
	if r.Reference.End.After(r.Incident.Start) {
		return errors.New("the reference window must end before the incident window starts")
	}
	if r.Incident.End.Sub(r.Incident.Start) > 14*24*time.Hour || r.Reference.End.Sub(r.Reference.Start) > 14*24*time.Hour {
		return errors.New("a window can be at most 14 days long")
	}
	return nil
}

// LaneCheck is one check's outcome. Fix is set when the state is "Cannot test yet" because of a missing grant or a switched-off feature.
type LaneCheck struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Summary string    `json:"summary"`
	Feeds   []string  `json:"feeds,omitempty"` // catalog entries this check can settle
	Fix     *GrantFix `json:"fix,omitempty"`
}

// LaneResult is the lane part of a Report.
type LaneResult struct {
	Incident  Window            `json:"incident"`
	Reference Window            `json:"reference"`
	Era       string            `json:"era,omitempty"` // SQL only: 2019 or 2022
	Checks    []LaneCheck       `json:"checks"`
	Answers   map[string]string `json:"answers,omitempty"`
	Onset     *time.Time        `json:"onset,omitempty"`
	Sources   []string          `json:"sources,omitempty"`
	Guide     *ProfilerGuide    `json:"guide,omitempty"` // Mongo only: set when the profiler is off
}

// State is the lane's readiness: Ready when every check ran, Partly ready when some did, otherwise Cannot test yet (or Failed when every check failed).
func (l LaneResult) State() string {
	ready, failed := 0, 0
	for _, c := range l.Checks {
		switch c.State {
		case StateReady:
			ready++
		case StateFailed:
			failed++
		}
	}
	switch {
	case len(l.Checks) > 0 && ready == len(l.Checks):
		return StateReady
	case ready > 0:
		return "Partly ready"
	case len(l.Checks) > 0 && failed == len(l.Checks):
		return StateFailed
	}
	return StateNotYet
}

// laneBuilder collects answers while the checks run. A yes can never be turned back into a no by a later check: the lane keeps what supports a
// suspect, and a no needs every check that touches the entry to agree.
type laneBuilder struct {
	res     LaneResult
	onset   *time.Time
	sources map[string]bool
}

func newLaneBuilder(req LaneRequest) *laneBuilder {
	return &laneBuilder{res: LaneResult{Incident: req.Incident, Reference: req.Reference, Answers: map[string]string{}}, sources: map[string]bool{}}
}

func (b *laneBuilder) answer(id, value string) {
	if value != AnswerYes && value != AnswerNo {
		return
	}
	if prev := b.res.Answers[id]; prev == AnswerYes {
		return
	}
	b.res.Answers[id] = value
}

func (b *laneBuilder) setOnset(t time.Time) {
	t = t.UTC()
	if b.onset == nil || t.Before(*b.onset) {
		b.onset = &t
	}
}

func (b *laneBuilder) source(name string) { b.sources[name] = true }

func (b *laneBuilder) add(c LaneCheck) { b.res.Checks = append(b.res.Checks, c) }

func (b *laneBuilder) finish() LaneResult {
	b.res.Onset = b.onset
	for s := range b.sources {
		b.res.Sources = append(b.res.Sources, s)
	}
	sort.Strings(b.res.Sources)
	if len(b.res.Answers) == 0 {
		b.res.Answers = nil
	}
	return b.res
}

// reportFromLane wraps a lane result as a Report so it is stored and listed with the other health runs. Every check becomes one finding.
func reportFromLane(kind, label string, req LaneRequest, version string, lane LaneResult, started time.Time, now clock) Report {
	rep := Report{Kind: kind, Label: label, Database: req.Database, Version: version, StartedAt: started, Lane: &lane}
	for _, c := range lane.Checks {
		f := Finding{ID: c.ID, Title: c.Title, Summary: c.Summary}
		switch c.State {
		case StateReady:
			f.Status = OK
		default:
			f.Status = Unavailable
		}
		rep.Findings = append(rep.Findings, f)
	}
	rep.Finish(now())
	if rep.Overall == OK {
		anyReady := false
		for _, c := range lane.Checks {
			if c.State == StateReady {
				anyReady = true
			}
		}
		if !anyReady {
			rep.Overall = Unavailable
		}
	}
	return rep
}

// notYetOrFailed turns a query error into the check state: a missing permission or a missing feature is "Cannot test yet" (the owner can fix it),
// anything else is "Failed". The driver's text is never kept.
func notYetOrFailed(reason string) string {
	if reason == reasonPermission || reason == reasonNotThere {
		return StateNotYet
	}
	return StateFailed
}

func rowTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case string:
		s := strings.TrimSpace(x)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.9999999 -07:00", "2006-01-02 15:04:05.9999999", "2006-01-02T15:04:05.9999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// wall reads a time the driver returned for a server-local column: the clock face is local to the server, so the UTC offset (minutes, local minus
// UTC) is taken off to get UTC.
func wall(t time.Time, offsetMin float64) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	return t.Add(-time.Duration(offsetMin) * time.Minute)
}

// sqlLit is a datetime2 literal built from a time this package formatted itself, so it cannot carry anything else into a query.
func sqlLit(t time.Time) string { return "'" + t.UTC().Format("2006-01-02T15:04:05") + "'" }

// sqlOffsetLit is the same as a datetimeoffset literal, for Query Store's interval times.
func sqlOffsetLit(t time.Time) string {
	return "CAST('" + t.UTC().Format("2006-01-02T15:04:05") + "+00:00' AS datetimeoffset(0))"
}
