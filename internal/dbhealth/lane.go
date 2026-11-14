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

// MaxLaneWindow is the longest one window (incident or reference) may be. A longer window is refused before any query is built, so a mistyped date
// cannot make a run cut thousands of slices (DATA-08).
const MaxLaneWindow = 90 * 24 * time.Hour

// Validate refuses a request the checks cannot compare: both windows must be real, neither may be longer than MaxLaneWindow, and the reference must
// end before the incident starts.
func (r LaneRequest) Validate() error {
	if !r.Incident.Valid() || !r.Reference.Valid() {
		return errors.New("both the incident window and the reference window need a start and an end")
	}
	if r.Incident.End.Sub(r.Incident.Start) > MaxLaneWindow {
		return errors.New("the incident window can be at most 90 days long; pick a shorter span")
	}
	if r.Reference.End.Sub(r.Reference.Start) > MaxLaneWindow {
		return errors.New("the reference window can be at most 90 days long; pick a shorter span")
	}
	if r.Reference.End.After(r.Incident.Start) {
		return errors.New("the reference window must end before the incident window starts")
	}
	return nil
}

// LaneRunTimeout bounds one whole lane run. A long window (a month or more) is read in slices, each with its own time limit, so the run as a
// whole is allowed to take much longer than one query.
const LaneRunTimeout = 20 * time.Minute

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
	// Series is the Query Store interval series of both windows (SQL only): the
	// Timeline draws the SQL lane from it when no SQL file was imported.
	Series []LaneSeriesPoint `json:"series,omitempty"`
	// QueryStore says what state Query Store was in when the lane read it (SQL only).
	QueryStore *LaneQueryStore `json:"queryStore,omitempty"`
}

// Query Store states a lane day can carry.
const (
	QSStateOn       = "on"
	QSStateOff      = "off"
	QSStateReadOnly = "read-only"
	QSStateDenied   = "denied"
	QSStateUnknown  = "unknown"
)

// LaneQueryStore is what the lane read learned about Query Store on the database.
type LaneQueryStore struct {
	// State is one of "on", "off", "read-only", "denied", "unknown".
	State string `json:"state"`
	// FirstData is the start of the oldest saved block, when one was found.
	FirstData *time.Time `json:"firstData,omitempty"`
	// IntervalMinutes is the block length Query Store is set to, when known.
	IntervalMinutes int `json:"intervalMinutes,omitempty"`
}

// LaneSeriesPoint is one Query Store interval: when it started, how many
// statements ran in it, and their average and slowest duration in milliseconds.
// The field names are short because a month-long window holds thousands of them.
type LaneSeriesPoint struct {
	T     int64   `json:"t"`           // interval start, Unix seconds
	Execs int64   `json:"n"`           // executions
	AvgMs float64 `json:"a"`           // average duration (ms), weighted by executions
	MaxMs float64 `json:"m,omitempty"` // slowest single execution (ms)
	// BlockSeconds is how much Query Store time this point covers: the sum of the blocks that were merged
	// into it. Zero means one block (the block length the run recorded), so a point that was never merged
	// carries nothing and older saved runs read as before. Busy time (average x executions) spread over
	// BlockSeconds gives the true busy per second of a merged point.
	BlockSeconds int64 `json:"b,omitempty"`
}

// laneSeriesCap bounds the saved series so a report stays far under MaxHealthReport.
const laneSeriesCap = 3000

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

// setSeries keeps the interval series for the saved report, oldest first and at
// most laneSeriesCap points. A longer series is thinned evenly over each window
// (neighbouring points are merged, never across the gap between the windows),
// never cut at one end.
func (b *laneBuilder) setSeries(pts []LaneSeriesPoint) {
	sort.Slice(pts, func(i, j int) bool { return pts[i].T < pts[j].T })
	var unit int64
	if qs := b.res.QueryStore; qs != nil && qs.IntervalMinutes > 0 {
		unit = int64(qs.IntervalMinutes) * 60
	}
	b.res.Series = downsampleWindows(pts, laneSeriesCap, unit, []Window{b.res.Reference, b.res.Incident})
}

// downsampleSeries merges neighbouring points in equal groups until at most limit remain. It knows no windows, so it treats the series as one run
// and takes the block length from the smallest gap between points; the saved series goes through downsampleWindows instead.
func downsampleSeries(pts []LaneSeriesPoint, limit int) []LaneSeriesPoint {
	return downsampleWindows(pts, limit, 0, nil)
}

// downsampleWindows thins a series that is sorted by time to at most limit points. Each window is thinned on its own, so a merged point never spans
// the gap between the reference and the incident (DATA-04), and each window keeps a share of the limit in proportion to its points. Inside a window
// neighbouring points are merged in equal groups: executions add up, the average is weighted by executions, the slowest stays the slowest, the group
// starts where its first point did, and BlockSeconds is the sum of the blocks it covers, so busy time over BlockSeconds stays the true busy per
// second. unit is the length of one block in seconds (0 means work it out from the smallest gap between points).
func downsampleWindows(pts []LaneSeriesPoint, limit int, unit int64, wins []Window) []LaneSeriesPoint {
	if limit <= 0 || len(pts) <= limit {
		return pts
	}
	if unit <= 0 {
		unit = smallestGap(pts)
	}
	// One run per window, plus a last run for points outside every window. The input is sorted, so each run is a stretch of it.
	runs := make([][]LaneSeriesPoint, len(wins)+1)
	for _, p := range pts {
		k := len(wins)
		for i, w := range wins {
			if w.Contains(time.Unix(p.T, 0)) {
				k = i
				break
			}
		}
		runs[k] = append(runs[k], p)
	}
	out := make([]LaneSeriesPoint, 0, limit)
	for _, run := range runs {
		if len(run) == 0 {
			continue
		}
		share := max(1, int(int64(limit)*int64(len(run))/int64(len(pts))))
		out = append(out, mergeRun(run, share, unit)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// mergeRun merges one run in equal groups so that at most limit points remain.
func mergeRun(run []LaneSeriesPoint, limit int, unit int64) []LaneSeriesPoint {
	if len(run) <= limit {
		return run
	}
	group := (len(run) + limit - 1) / limit
	out := make([]LaneSeriesPoint, 0, (len(run)+group-1)/group)
	for i := 0; i < len(run); i += group {
		part := run[i:min(i+group, len(run))]
		if len(part) == 1 {
			out = append(out, part[0])
			continue
		}
		p := LaneSeriesPoint{T: part[0].T}
		var sumMs float64
		for _, q := range part {
			p.Execs += q.Execs
			sumMs += q.AvgMs * float64(q.Execs)
			p.MaxMs = max(p.MaxMs, q.MaxMs)
			if q.BlockSeconds > 0 {
				p.BlockSeconds += q.BlockSeconds
			} else {
				p.BlockSeconds += unit
			}
		}
		if p.Execs > 0 {
			p.AvgMs = round1(sumMs / float64(p.Execs))
		}
		out = append(out, p)
	}
	return out
}

// smallestGap is the smallest positive distance in seconds between neighbouring points of a series sorted by time: the block length when blocks
// are saved back to back. Zero when there is none.
func smallestGap(pts []LaneSeriesPoint) int64 {
	var gap int64
	for i := 1; i < len(pts); i++ {
		if d := pts[i].T - pts[i-1].T; d > 0 && (gap == 0 || d < gap) {
			gap = d
		}
	}
	return gap
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
