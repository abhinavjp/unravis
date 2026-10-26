package dbhealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Mongo runs one database command given as relaxed extended JSON and returns the reply.
type Mongo interface {
	Command(ctx context.Context, database, commandJSON string) (map[string]any, error)
}

// ErrNotReadOnly is returned for any command outside the fixed read-only list.
var ErrNotReadOnly = errors.New("dbhealth: command is not on the read-only list")

// ReadOnlyCommand accepts a command only when its first key is a read-only diagnostic: serverStatus, currentOp, replSetGetStatus, dbStats, collStats,
// listIndexes, profile with the value -1 (read the level, never set it), or find on system.profile. Every check goes through this before it reaches
// the server, so a mistake in a check cannot turn into a write.
func ReadOnlyCommand(commandJSON string) error {
	dec := json.NewDecoder(strings.NewReader(commandJSON))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ErrNotReadOnly
	}
	t, err := dec.Token()
	if err != nil {
		return ErrNotReadOnly
	}
	name, ok := t.(string)
	if !ok {
		return ErrNotReadOnly
	}
	var whole map[string]json.RawMessage
	if err := json.Unmarshal([]byte(commandJSON), &whole); err != nil {
		return ErrNotReadOnly
	}
	for key := range whole {
		switch strings.ToLower(key) {
		case "$where", "$function", "$accumulator", "$out", "$merge":
			return ErrNotReadOnly
		}
	}
	if bytes.Contains([]byte(commandJSON), []byte("$where")) || bytes.Contains([]byte(commandJSON), []byte("$function")) {
		return ErrNotReadOnly
	}
	switch name {
	case "serverStatus", "currentOp", "replSetGetStatus", "dbStats", "collStats", "listIndexes":
		return nil
	case "profile":
		if string(bytes.TrimSpace(whole["profile"])) == "-1" {
			return nil
		}
	case "find":
		var coll string
		if json.Unmarshal(whole["find"], &coll) == nil && coll == "system.profile" {
			return nil
		}
	}
	return ErrNotReadOnly
}

type guarded struct{ inner Mongo }

func (g guarded) Command(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	if err := ReadOnlyCommand(commandJSON); err != nil {
		return nil, err
	}
	return g.inner.Command(ctx, database, commandJSON)
}

type monStep struct {
	id, title, why string
	run            func(ctx context.Context, m Mongo, env *Env) Finding
}

func classifyMongo(err error) string {
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "not authorized") || strings.Contains(m, "unauthorized") || strings.Contains(m, "requires authentication") || strings.Contains(m, "permission"):
		return reasonPermission
	case strings.Contains(m, "no such command") || strings.Contains(m, "command not found") || strings.Contains(m, "namespace not found") || strings.Contains(m, "ns does not exist"):
		return reasonNotThere
	}
	return reasonFailed
}

// RunMongo runs the MongoDB checks. database and collection are the audit database and collection the person picked; either may be empty, in which
// case the checks that need them say so instead of guessing.
func RunMongo(ctx context.Context, m Mongo, label, database, collection string, now clock) Report {
	g := guarded{inner: m}
	env := &Env{Database: database, Collection: collection, Now: now()}
	rep := Report{Kind: "mongo", Label: label, Database: database, StartedAt: env.Now}
	for _, s := range mongoSteps() {
		if ctx.Err() != nil {
			break
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		f := s.run(cctx, g, env)
		cancel()
		f.ID, f.Title = s.id, s.title
		if f.Why == "" {
			f.Why = s.why
		}
		rep.Findings = append(rep.Findings, f)
	}
	rep.Version = env.Version
	rep.Finish(now())
	return rep
}

func mongoSteps() []monStep {
	return []monStep{
		{"server", "Server", "Version, uptime, connection use, queued operations and how full the write cache is. Connections near the limit or a long lock queue make every request slower.", runServerStatus},
		{"currentop", "Running now: slow operations", "Operations that have been running 5 seconds or more at this moment. A collection scan or an operation waiting for a lock is the usual cause of a slow API.", runCurrentOp},
		{"replication", "Replication", "Whether the copies of the data are healthy and how far behind they are. A lagging or unhealthy member means stale reads and no safety net if the main one fails.", runReplication},
		{"profiler", "Slow operations, last 24 hours", "The database profiler records operations slower than its threshold. Operations that scan the whole collection are the ones to fix first.", runProfiler},
		{"disk", "Disk and size", "How full the disk behind this database is, and how big its data and indexes are. A full disk stops writes.", runDBStats},
		{"index", "Timing index", "The audit queries filter by time. Without an index on RequestInitiatedAt (or the older CreatedOn) every search reads the whole collection.", runTimingIndex},
	}
}

/* ------------------------------------------------------------- helpers */

func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func list(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// jnum reads a number the way relaxed extended JSON may give it: a plain number or {"$numberLong": "…"} / {"$numberDouble": "…"}.
func jnum(v any) float64 {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"$numberLong", "$numberInt", "$numberDouble", "$numberDecimal"} {
			if s, ok := m[k]; ok {
				return num(s)
			}
		}
		return 0
	}
	return num(v)
}

func jdate(v any) (time.Time, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	switch d := m["$date"].(type) {
	case string:
		t, err := time.Parse(time.RFC3339Nano, d)
		return t, err == nil
	case map[string]any:
		ms := int64(jnum(d))
		return time.UnixMilli(ms).UTC(), true
	case float64:
		return time.UnixMilli(int64(d)).UTC(), true
	}
	return time.Time{}, false
}

func cmdJSON(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

/* --------------------------------------------------------------- checks */

func runServerStatus(ctx context.Context, m Mongo, env *Env) Finding {
	r, err := m.Command(ctx, "admin", `{"serverStatus":1,"metrics":0,"locks":0,"tcmalloc":0}`)
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalServerStatus(r, env)
}

func evalServerStatus(r map[string]any, env *Env) Finding {
	env.Version = text(r["version"])
	upSec := jnum(r["uptime"])
	env.UptimeMin = upSec / 60
	cur, avail := jnum(dig(r, "connections", "current")), jnum(dig(r, "connections", "available"))
	queue := jnum(dig(r, "globalLock", "currentQueue", "total"))
	maxCache := jnum(dig(r, "wiredTiger", "cache", "maximum bytes configured"))
	dirty := jnum(dig(r, "wiredTiger", "cache", "tracked dirty bytes in the cache"))
	connPct := pct(cur, cur+avail)
	f := Finding{Status: OK, Columns: []string{"Version", "Up for", "Connections", "Queued operations", "Cache dirty %"},
		Rows: [][]string{{env.Version, duration(env.UptimeMin), fmt.Sprintf("%s of %s", fmtInt(cur), fmtInt(cur+avail)), fmtInt(queue), fmtF(pct(dirty, maxCache), 1)}}}
	f.Summary = fmt.Sprintf("MongoDB %s, up for %s. %s%% of connections in use, %s operations queued.", env.Version, duration(env.UptimeMin), fmtInt(connPct), fmtInt(queue))
	var notes []string
	switch {
	case connPct >= 90:
		f.Status = Bad
		notes = append(notes, "connections are almost used up (new clients will be refused)")
	case connPct >= 70:
		f.Status = Warn
		notes = append(notes, "connection use is high")
	}
	if queue >= 10 {
		if f.Status == OK {
			f.Status = Warn
		}
		notes = append(notes, "operations are queuing behind locks")
	}
	if maxCache > 0 && pct(dirty, maxCache) > 20 {
		if f.Status == OK {
			f.Status = Warn
		}
		notes = append(notes, "the write cache is over 20% dirty (the disk is not keeping up)")
	}
	if upSec < 3600 {
		if f.Status == OK {
			f.Status = Warn
		}
		notes = append(notes, "it restarted less than an hour ago")
	}
	if len(notes) > 0 {
		f.Summary += " Note: " + strings.Join(notes, "; ") + "."
	}
	return f
}

func runCurrentOp(ctx context.Context, m Mongo, _ *Env) Finding {
	r, err := m.Command(ctx, "admin", `{"currentOp":1,"active":true,"secs_running":{"$gte":5}}`)
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalCurrentOp(list(r["inprog"]))
}

func evalCurrentOp(ops []map[string]any) Finding {
	var kept []map[string]any
	for _, o := range ops {
		if strings.HasPrefix(text(o["ns"]), "local.oplog") || text(o["op"]) == "getmore" && strings.HasPrefix(text(o["ns"]), "local.") {
			continue
		}
		kept = append(kept, o)
	}
	if len(kept) == 0 {
		return Finding{Status: OK, Summary: "No operation has been running for 5 seconds or more right now."}
	}
	sort.SliceStable(kept, func(i, j int) bool { return jnum(kept[i]["secs_running"]) > jnum(kept[j]["secs_running"]) })
	f := Finding{Status: Note, Columns: []string{"Operation", "Collection", "Seconds", "Plan", "Waiting for a lock"}}
	scans, waits := 0, 0
	for i, o := range kept {
		plan := text(o["planSummary"])
		wait := ""
		if b, _ := o["waitingForLock"].(bool); b {
			wait = "yes"
			waits++
		}
		if strings.HasPrefix(plan, "COLLSCAN") {
			scans++
		}
		if i < 10 {
			f.Rows = append(f.Rows, []string{text(o["op"]), text(o["ns"]), fmtInt(jnum(o["secs_running"])), plan, wait})
		}
	}
	f.Summary = plural(len(kept), "operation has", "operations have") + " been running 5 seconds or more right now."
	if scans > 0 || waits > 0 || jnum(kept[0]["secs_running"]) >= 60 {
		f.Status = Warn
		if scans > 0 {
			f.Summary += " " + plural(scans, "of them reads", "of them read") + " the whole collection (COLLSCAN)."
		}
		if waits > 0 {
			f.Summary += " " + plural(waits, "is", "are") + " waiting for a lock."
		}
	}
	return f
}

func runReplication(ctx context.Context, m Mongo, _ *Env) Finding {
	r, err := m.Command(ctx, "admin", `{"replSetGetStatus":1}`)
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not running with --replset") || strings.Contains(low, "noreplicationenabled") || strings.Contains(low, "replication is not enabled") {
			return Finding{Status: Note, Summary: "This MongoDB is a single server (no replica set), so there is no replication to check."}
		}
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalReplication(r)
}

func evalReplication(r map[string]any) Finding {
	members := list(r["members"])
	if len(members) == 0 {
		return Finding{Status: Note, Summary: "No replica set members reported."}
	}
	var primary time.Time
	havePrimary := false
	for _, mb := range members {
		if text(mb["stateStr"]) == "PRIMARY" {
			if t, ok := jdate(mb["optimeDate"]); ok {
				primary, havePrimary = t, true
			} else {
				havePrimary = true
			}
		}
	}
	f := Finding{Status: OK, Columns: []string{"Member", "State", "Healthy", "Behind (s)"}}
	worst := 0.0
	unhealthy := 0
	for _, mb := range members {
		state := text(mb["stateStr"])
		healthy := jnum(mb["health"]) == 1
		if !healthy {
			unhealthy++
		}
		lag := ""
		if t, ok := jdate(mb["optimeDate"]); ok && state == "SECONDARY" && !primary.IsZero() {
			d := primary.Sub(t).Seconds()
			if d < 0 {
				d = 0
			}
			lag = fmtInt(d)
			if d > worst {
				worst = d
			}
		}
		yes := "yes"
		if !healthy {
			yes = "NO"
		}
		f.Rows = append(f.Rows, []string{text(mb["name"]), state, yes, lag})
	}
	f.Summary = plural(len(members), "member", "members") + " in the replica set."
	switch {
	case !havePrimary:
		f.Status = Bad
		f.Summary += " There is no primary right now (an election may be running)."
	case unhealthy > 0:
		f.Status = Bad
		f.Summary += " " + plural(unhealthy, "member is", "members are") + " not healthy."
	case worst > 60:
		f.Status = Bad
		f.Summary += " The slowest secondary is " + fmtInt(worst) + " seconds behind."
	case worst > 10:
		f.Status = Warn
		f.Summary += " The slowest secondary is " + fmtInt(worst) + " seconds behind."
	default:
		f.Summary += " All healthy and in step."
	}
	return f
}

func runProfiler(ctx context.Context, m Mongo, env *Env) Finding {
	if env.Database == "" {
		return Finding{Status: Note, Summary: "Pick the audit database to read its profiler."}
	}
	lvl, err := m.Command(ctx, env.Database, `{"profile":-1}`)
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	if jnum(lvl["was"]) == 0 {
		return Finding{Status: Note, Summary: "The profiler is off for this database, so slow operations are not being recorded. A database owner can turn it on with db.setProfilingLevel(1, 100); it adds a little overhead."}
	}
	since := env.Now.Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	q := map[string]any{
		"find":       "system.profile",
		"filter":     map[string]any{"millis": map[string]any{"$gte": 100}, "ts": map[string]any{"$gte": map[string]any{"$date": since}}},
		"sort":       map[string]any{"millis": -1},
		"limit":      200,
		"projection": map[string]any{"op": 1, "ns": 1, "millis": 1, "planSummary": 1, "docsExamined": 1, "nreturned": 1},
	}
	r, err := m.Command(ctx, env.Database, cmdJSON(q))
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalProfiler(list(dig(r, "cursor", "firstBatch")))
}

func evalProfiler(docs []map[string]any) Finding {
	if len(docs) == 0 {
		return Finding{Status: OK, Summary: "The profiler is on and recorded no operation slower than 100 ms in the last 24 hours."}
	}
	type group struct {
		op, ns       string
		n            int
		sum, max     float64
		scan         bool
		examined, ret float64
	}
	by := map[string]*group{}
	for _, d := range docs {
		key := text(d["op"]) + "|" + text(d["ns"])
		g := by[key]
		if g == nil {
			g = &group{op: text(d["op"]), ns: text(d["ns"])}
			by[key] = g
		}
		ms := jnum(d["millis"])
		g.n++
		g.sum += ms
		if ms > g.max {
			g.max = ms
		}
		if strings.HasPrefix(text(d["planSummary"]), "COLLSCAN") {
			g.scan = true
		}
		g.examined += jnum(d["docsExamined"])
		g.ret += jnum(d["nreturned"])
	}
	groups := make([]*group, 0, len(by))
	for _, g := range by {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].sum > groups[j].sum })
	f := Finding{Status: Note, Columns: []string{"Operation", "Collection", "Count", "Avg ms", "Max ms", "Scans whole collection", "Docs read per doc returned"}}
	scans := 0
	for i, g := range groups {
		scan := ""
		if g.scan {
			scan = "yes"
			scans++
		}
		ratio := ""
		if g.ret > 0 {
			ratio = fmtInt(g.examined / g.ret)
		} else if g.examined > 0 {
			ratio = "all"
		}
		if i < 10 {
			f.Rows = append(f.Rows, []string{g.op, g.ns, fmtInt(float64(g.n)), fmtInt(g.sum / float64(g.n)), fmtInt(g.max), scan, ratio})
		}
	}
	f.Summary = plural(len(docs), "operation", "operations") + " took 100 ms or more in the last 24 hours."
	if scans > 0 {
		f.Status = Warn
		f.Summary += " " + plural(scans, "kind reads", "kinds read") + " the whole collection; an index would fix that."
	}
	return f
}

func runDBStats(ctx context.Context, m Mongo, env *Env) Finding {
	if env.Database == "" {
		return Finding{Status: Note, Summary: "Pick the audit database to see its size and disk."}
	}
	r, err := m.Command(ctx, env.Database, `{"dbStats":1,"scale":1048576}`)
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalDBStats(r)
}

func evalDBStats(r map[string]any) Finding {
	data, index := jnum(r["dataSize"]), jnum(r["indexSize"])
	used, total := jnum(r["fsUsedSize"]), jnum(r["fsTotalSize"])
	f := Finding{Status: OK, Columns: []string{"Data MB", "Indexes MB", "Disk used MB", "Disk total MB"},
		Rows: [][]string{{fmtInt(data), fmtInt(index), fmtInt(used), fmtInt(total)}}}
	f.Summary = fmt.Sprintf("Data %s MB, indexes %s MB.", fmtInt(data), fmtInt(index))
	if total > 0 {
		p := pct(used, total)
		f.Summary += fmt.Sprintf(" The disk is %s%% full.", fmtInt(p))
		switch {
		case p >= 95:
			f.Status = Bad
		case p >= 85:
			f.Status = Warn
		}
	}
	return f
}

func runTimingIndex(ctx context.Context, m Mongo, env *Env) Finding {
	if env.Database == "" || env.Collection == "" {
		return Finding{Status: Note, Summary: "Pick the audit database and collection to check for a timing index."}
	}
	b, _ := json.Marshal(env.Collection)
	r, err := m.Command(ctx, env.Database, `{"listIndexes":`+string(b)+`}`)
	if err != nil {
		return unavailable("", "", "", classifyMongo(err))
	}
	return evalTimingIndex(list(dig(r, "cursor", "firstBatch")))
}

func evalTimingIndex(indexes []map[string]any) Finding {
	for _, ix := range indexes {
		if key, ok := ix["key"].(map[string]any); ok {
			for k := range key {
				if k == "RequestInitiatedAt" || k == "CreatedOn" {
					return Finding{Status: OK, Summary: "The collection has an index on " + k + ", so time-window searches do not read everything."}
				}
			}
		}
	}
	return Finding{Status: Warn, Summary: "No index on RequestInitiatedAt (or CreatedOn) was found. Every time-window search reads the whole collection; ask the database owner to add one."}
}
