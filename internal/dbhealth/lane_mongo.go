package dbhealth

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// The Mongo lane runs with clusterMonitor on admin and read on the audit database (decision A29). It sends only commands from the read-only list
// (ReadOnlyCommand): the profile command with -1 reads the level and never sets it. Production MongoDB is a standalone server, so there is no
// replication check here. The other Mongo entries (cache, tickets, connections, disk) need a time series that a point-in-time read cannot give.

const (
	CheckMongoServer   = "server"
	CheckMongoProfiler = "profiler"
)

const mongoMaxProfileDocs = 5000

type mongoLane struct {
	m   Mongo
	req LaneRequest
	b   *laneBuilder
}

// RunMongoLane runs the Mongo checks for one incident and returns them as a report whose Lane holds the answers.
func RunMongoLane(ctx context.Context, m Mongo, label string, req LaneRequest, now clock) Report {
	started := now()
	l := &mongoLane{m: guarded{inner: m}, req: req, b: newLaneBuilder(req)}
	version := ""
	steps := []struct {
		id, title string
		feeds     []string
		run       func(context.Context) LaneCheck
	}{
		{CheckMongoServer, "Server", nil, func(ctx context.Context) LaneCheck {
			c, v := l.checkServer(ctx)
			version = v
			return c
		}},
		{CheckMongoProfiler, "Slow operations (profiler)", []string{"M6"}, l.checkProfiler},
	}
	for _, st := range steps {
		var c LaneCheck
		if ctx.Err() != nil {
			c = LaneCheck{State: StateFailed, Summary: "The run stopped before this check ran."}
		} else {
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			c = st.run(cctx)
			cancel()
		}
		c.ID, c.Title, c.Feeds = st.id, st.title, st.feeds
		if c.State == StateReady {
			l.b.source("MongoDB: " + st.title)
		}
		l.b.add(c)
	}
	return reportFromLane("mongo", label, req, version, l.b.finish(), started, now)
}

func (l *mongoLane) failed(err error, permission string) LaneCheck {
	reason := classifyMongo(err)
	c := LaneCheck{State: notYetOrFailed(reason), Summary: reason}
	if reason == reasonPermission {
		c.Summary = "Cannot read this yet: the login is missing " + permission + ". The script below, run by the MongoDB administrator, adds it."
		c.Fix = &GrantFix{Permission: permission, Script: MongoGrantScript(l.req.Login, l.req.Database)}
	}
	return c
}

func (l *mongoLane) checkServer(ctx context.Context) (LaneCheck, string) {
	r, err := l.m.Command(ctx, "admin", `{"serverStatus":1,"metrics":0,"locks":0,"tcmalloc":0}`)
	if err != nil {
		return l.failed(err, "clusterMonitor on the admin database"), ""
	}
	version := text(r["version"])
	parts := []string{"MongoDB " + version + "."}
	if up := jnum(r["uptime"]); up > 0 {
		parts = append(parts, "Running for "+duration(up/60)+".")
	}
	if dig(r, "repl") == nil {
		parts = append(parts, "A standalone server, so there is no replication to check.")
	}
	return LaneCheck{State: StateReady, Summary: strings.Join(parts, " ")}, version
}

type profileOutcome struct {
	check  LaneCheck
	answer string // M6
	onset  *time.Time
}

func profileFind(req LaneRequest, limit int, ascending bool) string {
	sortDir := 1
	if !ascending {
		sortDir = -1
	}
	// Built by hand, not marshalled: ReadOnlyCommand reads the first key as the command name, and a marshalled map sorts its keys.
	return `{"find":"system.profile","filter":{"ts":{"$gte":{"$date":"` + req.Reference.Start.UTC().Format(time.RFC3339) +
		`"},"$lt":{"$date":"` + req.Incident.End.UTC().Format(time.RFC3339) + `"}}},"sort":{"ts":` + strconv.Itoa(sortDir) +
		`},"limit":` + strconv.Itoa(limit) + `,"projection":{"op":1,"ns":1,"millis":1,"planSummary":1,"ts":1}}`
}

const profileOldest = `{"find":"system.profile","filter":{},"sort":{"ts":1},"limit":1,"projection":{"ts":1}}`

// evalLaneProfiler reads the slow operations in both windows. oldest is when the profile collection's first record was written (zero when it is
// empty): the profiler cannot describe anything before that.
func evalLaneProfiler(slowms float64, oldest time.Time, docs []map[string]any, req LaneRequest) profileOutcome {
	var o profileOutcome
	if !oldest.IsZero() && !oldest.Before(req.Incident.End) {
		o.check = LaneCheck{State: StateNotYet, Summary: "The profiler started recording at " + utcText(oldest) + ", after the incident ended, so it holds nothing from it."}
		return o
	}
	ns := ""
	if req.Collection != "" {
		ns = req.Database + "." + req.Collection
	}
	audit := func(n string) bool {
		if ns != "" {
			return n == ns
		}
		return strings.HasPrefix(n, req.Database+".") && !strings.HasPrefix(n, req.Database+".system.")
	}
	refN, incN, scans, indexed := 0, 0, 0, 0
	perMinute := map[time.Time]int{}
	for _, d := range docs {
		t, ok := jdate(d["ts"])
		if !ok || !audit(text(d["ns"])) {
			continue
		}
		t = t.UTC()
		switch {
		case req.Incident.Contains(t):
			incN++
			perMinute[t.Truncate(time.Minute)]++
			plan := text(d["planSummary"])
			if strings.HasPrefix(plan, "COLLSCAN") {
				scans++
			} else if plan != "" {
				indexed++
			}
		case req.Reference.Contains(t):
			refN++
		}
	}
	// The profiler covers the whole incident only when it was already recording when the incident began.
	wholeIncident := !oldest.IsZero() && !oldest.After(req.Incident.Start)
	coversReference := !oldest.IsZero() && !oldest.After(req.Reference.Start)
	var parts []string
	threshold := "100 ms"
	if slowms > 0 {
		threshold = fmtInt(slowms) + " ms"
	}
	parts = append(parts, "The profiler records operations slower than "+threshold+".")
	parts = append(parts, plural(incN, "slow operation", "slow operations")+" in the incident window and "+plural(refN, "slow operation", "slow operations")+" in the reference window on the audit collection.")
	switch {
	case scans > 0:
		o.answer = AnswerYes
		parts = append(parts, plural(scans, "read", "reads")+" the whole collection.")
	case indexed > 0 && wholeIncident:
		o.answer = AnswerNo
		parts = append(parts, "Every slow operation used an index.")
	}
	if !wholeIncident && !oldest.IsZero() {
		parts = append(parts, "It only started recording at "+utcText(oldest)+", after the incident began, so it holds part of the incident.")
	}
	if oldest.IsZero() {
		parts = append(parts, "Its record is empty, so it cannot say how long it has been on.")
	}
	if coversReference && incN > 0 {
		minutes := req.Reference.End.Sub(req.Reference.Start).Minutes()
		rate := 0.0
		if minutes > 0 {
			rate = float64(refN) / minutes
		}
		need := 2 * rate
		if need < 3 {
			need = 3
		}
		var first time.Time
		for t, n := range perMinute {
			if float64(n) >= need && (first.IsZero() || t.Before(first)) {
				first = t
			}
		}
		if !first.IsZero() {
			o.onset = &first
			parts = append(parts, "Slow operations first piled up at "+utcText(first)+".")
		}
	}
	o.check = LaneCheck{State: StateReady, Summary: strings.Join(parts, " ")}
	return o
}

func (l *mongoLane) checkProfiler(ctx context.Context) LaneCheck {
	if l.req.Database == "" {
		return LaneCheck{State: StateNotYet, Summary: "Pick the audit database. The profiler is read one database at a time."}
	}
	perm := "read on the " + l.req.Database + " database"
	lvl, err := l.m.Command(ctx, l.req.Database, `{"profile":-1}`)
	if err != nil {
		return l.failed(err, perm)
	}
	if jnum(lvl["was"]) == 0 {
		guide := MongoProfilerGuide(l.req.Database)
		l.b.res.Guide = &guide
		return LaneCheck{State: StateNotYet,
			Summary: "The profiler is off for this database, so slow operations were not recorded. The guide below shows how to switch it on; it only helps for incidents after that.",
			Fix:     &GrantFix{Permission: "Profiler switched on", Script: guide.SwitchOn}}
	}
	var oldest time.Time
	r, err := l.m.Command(ctx, l.req.Database, profileOldest)
	if err != nil {
		return l.failed(err, perm)
	}
	if first := list(dig(r, "cursor", "firstBatch")); len(first) > 0 {
		oldest, _ = jdate(first[0]["ts"])
		oldest = oldest.UTC()
	}
	r, err = l.m.Command(ctx, l.req.Database, profileFind(l.req, mongoMaxProfileDocs, true))
	if err != nil {
		return l.failed(err, perm)
	}
	o := evalLaneProfiler(jnum(lvl["slowms"]), oldest, list(dig(r, "cursor", "firstBatch")), l.req)
	if o.check.State == StateReady {
		if o.answer != "" {
			l.b.answer("M6", o.answer)
		}
		if o.onset != nil {
			l.b.setOnset(*o.onset)
		}
	}
	return o.check
}
