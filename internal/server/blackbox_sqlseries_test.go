package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
)

// SQL charts: the SQL service's samples drawn beside the host counters.

var sqlSeriesT0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func sqlWin() bbWindow {
	return bbWindow{Start: sqlSeriesT0, End: sqlSeriesT0.Add(30 * time.Minute)}
}

func sqlAns(samples []blackbox.SQLSample, rep blackbox.SQLReadReport) bbSeriesAnswer {
	host := []blackbox.Sample{{V: 1, Run: "r", Seq: 1, T: sqlSeriesT0}}
	return buildBBSeriesWith(blackbox.Manifest{IntervalSeconds: 15}, host, blackbox.ReadReport{}, blackbox.StateInfo{}, sqlWin(), 30, false, false,
		&bbSQLRead{Samples: samples, Report: rep})
}

func lineOf(a bbSeriesAnswer, key string) *bbSeriesLine {
	for i := range a.Lines {
		if a.Lines[i].Key == key {
			return &a.Lines[i]
		}
	}
	return nil
}

func at(min int) time.Time { return sqlSeriesT0.Add(time.Duration(min) * time.Minute) }

func i32(v int32) *int32 { return &v }

func TestSQLSeriesEveryKindBecomesLines(t *testing.T) {
	s := []blackbox.SQLSample{
		{T: at(1), Kind: "requests", Requests: []bbsql.Requests{
			{WaitType: "RUNNING", Tasks: 5}, {WaitType: "LCK_M_X", Tasks: 3, MaxWaitMs: 900, Blocked: 3, HeadBlockers: 1, MaxBlockMs: 800}}},
		{T: at(1), Kind: "waits", Waits: []bbsql.Waits{{WaitType: "PAGEIOLATCH_SH", DeltaMs: 1200, DeltaTasks: 40}}},
		{T: at(1), Kind: "schedulers", Schedulers: &bbsql.Schedulers{CurrentTasks: 12, RunnableTasks: 2}},
		{T: at(1), Kind: "semaphores", Semaphores: []bbsql.Semaphores{
			{PoolID: i32(2), ResourceSemaphoreID: 1, Grantees: 3, Waiters: 4, Forced: i32(1), Timeouts: i32(0)}}},
		{T: at(1), Kind: "tempdb", Tempdb: &bbsql.Tempdb{InternalPages: 100, UserPages: 200}},
		{T: at(1), Kind: "filestats", FileStats: []bbsql.FileStats{
			{DatabaseID: 5, FileID: 1, ReadStallMs: 100, WriteStallMs: 30, Reads: 50, Writes: 10},
			{DatabaseID: 5, FileID: 2, ReadStallMs: 20, WriteStallMs: 10, Reads: 50, Writes: 10}}},
		{T: at(1), Kind: "ag", AG: []bbsql.AG{{DatabaseID: 5, SyncState: 2}, {DatabaseID: 6, SyncState: 0}, {DatabaseID: 7, SyncState: 4}}},
	}
	a := sqlAns(s, blackbox.SQLReadReport{Files: 1, Samples: len(s)})
	want := map[string]float64{
		"sqlDmv.requests.tasks": 8, "sqlDmv.requests.blocked": 3, "sqlDmv.requests.headBlockers": 1, "sqlDmv.requests.maxWaitMs": 900, "sqlDmv.requests.maxBlockMs": 800,
		"sqlDmv.requestsByWait.LCK_M_X.tasks": 3, "sqlDmv.requestsByWait.RUNNING.tasks": 5,
		"sqlDmv.waits.PAGEIOLATCH_SH.deltaMs": 1200, "sqlDmv.waits.PAGEIOLATCH_SH.deltaTasks": 40,
		"sqlDmv.schedulers.currentTasks": 12, "sqlDmv.schedulers.runnableTasks": 2,
		"sqlDmv.semaphores.pool2sem1.grantees": 3, "sqlDmv.semaphores.pool2sem1.waiters": 4, "sqlDmv.semaphores.pool2sem1.forced": 1, "sqlDmv.semaphores.pool2sem1.timeouts": 0,
		"sqlDmv.tempdb.internalBytes": 100 * 8192, "sqlDmv.tempdb.userBytes": 200 * 8192,
		"sqlDmv.fileStats.readLatencyMs": 1.2, "sqlDmv.fileStats.writeLatencyMs": 2, "sqlDmv.fileStats.reads": 100, "sqlDmv.fileStats.writes": 20,
		"sqlDmv.fileStats.db5.readLatencyMs": 1.2, "sqlDmv.fileStats.db5.writeLatencyMs": 2,
		"sqlDmv.ag.databases": 3, "sqlDmv.ag.notSynchronizing": 1, "sqlDmv.ag.synchronizing": 0, "sqlDmv.ag.synchronized": 1, "sqlDmv.ag.other": 1,
	}
	for k, v := range want {
		l := lineOf(a, k)
		if l == nil || len(l.Points) != 1 {
			t.Errorf("%s: %+v (have %v)", k, l, keysOf(a.Lines))
			continue
		}
		if got := l.Points[0][1]; got < v-0.0001 || got > v+0.0001 {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
		if l.Group != "sqlDmv" {
			t.Errorf("%s group = %q", k, l.Group)
		}
	}
	for _, l := range a.Lines {
		if strings.HasPrefix(l.Key, "sqlDmv.") {
			if _, ok := want[l.Key]; !ok {
				t.Errorf("unexpected line %s", l.Key)
			}
		}
	}
}

func TestSQLSeriesUnitsAreRight(t *testing.T) {
	cases := map[string]string{
		"sqlDmv.requests.maxWaitMs": "ms", "sqlDmv.requests.tasks": "count", "sqlDmv.waits.X.deltaMs": "ms", "sqlDmv.waits.X.deltaTasks": "count",
		"sqlDmv.tempdb.internalBytes": "bytes", "sqlDmv.fileStats.readLatencyMs": "ms", "sqlDmv.fileStats.db3.writeLatencyMs": "ms",
		"sqlDmv.semaphores.pool1sem0.grantees": "count", "sqlDmv.ag.synchronized": "count",
	}
	for k, want := range cases {
		if got := bbSeriesUnit(k); got != want {
			t.Errorf("%s unit = %s, want %s", k, got, want)
		}
	}
}

func TestSQLSeriesNeverInventsAReading(t *testing.T) {
	s := []blackbox.SQLSample{
		{T: at(1), Kind: "requests"},   // nothing was running: no rows
		{T: at(1), Kind: "waits"},      // first cycle: no baseline
		{T: at(1), Kind: "schedulers"}, // the sum was NULL
		{T: at(1), Kind: "tempdb"},
		{T: at(1), Kind: "filestats"},
		{T: at(1), Kind: "ag"},
		{T: at(2), Kind: "semaphores", Semaphores: []bbsql.Semaphores{{PoolID: nil, ResourceSemaphoreID: 0, Grantees: 1, Waiters: 1}}},    // unreadable pool
		{T: at(3), Kind: "semaphores", Semaphores: []bbsql.Semaphores{{PoolID: i32(1), ResourceSemaphoreID: 0, Grantees: 1, Waiters: 0}}}, // delta counters unreadable
		{T: at(4), Kind: "filestats", FileStats: []bbsql.FileStats{{DatabaseID: 5, FileID: 1, ReadStallMs: 5, Reads: 0, Writes: 0}}},      // no reads: no latency
	}
	a := sqlAns(s, blackbox.SQLReadReport{Files: 1})
	for _, l := range a.Lines {
		if !strings.HasPrefix(l.Key, "sqlDmv.") {
			continue
		}
		switch l.Key {
		case "sqlDmv.semaphores.pool1sem0.grantees", "sqlDmv.semaphores.pool1sem0.waiters", "sqlDmv.fileStats.reads", "sqlDmv.fileStats.writes":
		default:
			t.Errorf("a reading appeared from nothing: %s %v", l.Key, l.Points)
		}
	}
	if l := lineOf(a, "sqlDmv.semaphores.pool1sem0.forced"); l != nil {
		t.Errorf("an unreadable counter was drawn: %+v", l)
	}
	if l := lineOf(a, "sqlDmv.fileStats.readLatencyMs"); l != nil {
		t.Errorf("a latency over zero reads was drawn: %+v", l)
	}
	for _, l := range a.Lines {
		if strings.Contains(l.Key, "pool0") || strings.Contains(l.Key, "poolnil") {
			t.Errorf("a missing pool id became a pool: %s", l.Key)
		}
	}
}

func TestSQLSeriesKeepsTheBusiestWaitsAndDatabasesOnly(t *testing.T) {
	var waits []bbsql.Waits
	var files []bbsql.FileStats
	for i := 0; i < bbsql.CapWaits; i++ {
		waits = append(waits, bbsql.Waits{WaitType: "W" + strconv.Itoa(i), DeltaMs: uint64(i + 1), DeltaTasks: 1})
	}
	for i := 0; i < 60; i++ {
		files = append(files, bbsql.FileStats{DatabaseID: uint32(i), FileID: 1, ReadStallMs: uint64(i + 1), Reads: 1, Writes: 1})
	}
	var s []blackbox.SQLSample
	for m := 0; m < 20; m++ {
		// A different set of wait types in each minute, so the window holds 20 x 20 distinct types.
		var ws []bbsql.Waits
		for i := 0; i < bbsql.CapWaits; i++ {
			ws = append(ws, bbsql.Waits{WaitType: "W" + strconv.Itoa(m*100+i), DeltaMs: uint64(m*100 + i + 1), DeltaTasks: 1})
		}
		s = append(s, blackbox.SQLSample{T: at(m), Kind: "waits", Waits: ws}, blackbox.SQLSample{T: at(m), Kind: "filestats", FileStats: files})
	}
	_ = waits
	a := sqlAns(s, blackbox.SQLReadReport{})
	nWait, nDB := map[string]bool{}, map[string]bool{}
	for _, l := range a.Lines {
		parts := strings.Split(l.Key, ".")
		if len(parts) == 4 && parts[1] == "waits" {
			nWait[parts[2]] = true
		}
		if len(parts) == 4 && parts[1] == "fileStats" {
			nDB[parts[2]] = true
		}
	}
	if len(nWait) != bbSQLMaxWaitLines || len(nDB) != bbSQLMaxDBLines {
		t.Fatalf("wait types %d (cap %d), databases %d (cap %d)", len(nWait), bbSQLMaxWaitLines, len(nDB), bbSQLMaxDBLines)
	}
	// The kept ones are the busiest: the biggest wait type of the window is there, the smallest is not.
	if !nWait["W1919"] || nWait["W0"] || !nDB["db59"] || nDB["db0"] {
		t.Fatalf("not the busiest: %v %v", sortedKeys(nWait), sortedKeys(nDB))
	}
	if a.Truncated {
		t.Error("a capped list is not a truncated answer")
	}
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSQLSeriesKeysAreNeverText(t *testing.T) {
	// Whatever the writer stored, a key is built from a validated wait type, a number or a fixed word: no space, quote or slash.
	re := regexp.MustCompile(`^sqlDmv(\.[A-Za-z0-9_]+)+$`)
	s := []blackbox.SQLSample{
		{T: at(1), Kind: "requests", Requests: []bbsql.Requests{{WaitType: "RUNNING", Tasks: 1}, {WaitType: "LCK_M_S", Tasks: 1}}},
		{T: at(1), Kind: "waits", Waits: []bbsql.Waits{{WaitType: "CXPACKET", DeltaMs: 1, DeltaTasks: 1}}},
		{T: at(1), Kind: "filestats", FileStats: []bbsql.FileStats{{DatabaseID: 4294967295, FileID: 1, Reads: 1, Writes: 1}}},
		{T: at(1), Kind: "semaphores", Semaphores: []bbsql.Semaphores{{PoolID: i32(2147483647), ResourceSemaphoreID: -1, Grantees: 1}}},
	}
	for _, l := range sqlAns(s, blackbox.SQLReadReport{}).Lines {
		if strings.HasPrefix(l.Key, "sqlDmv") && !re.MatchString(l.Key) {
			t.Errorf("key %q is not plain", l.Key)
		}
	}
}

func TestSQLSeriesCoverAndSummary(t *testing.T) {
	s := []blackbox.SQLSample{
		{T: at(1), Kind: "tempdb", Tempdb: &bbsql.Tempdb{InternalPages: 1}},
		{T: at(2), Kind: "requests", Truncated: true, Requests: []bbsql.Requests{{WaitType: "RUNNING", Tasks: 1}}},
	}
	a := sqlAns(s, blackbox.SQLReadReport{Files: 2, Missing: 1, Corrupt: 3, Locked: true})
	if a.SQL == nil || a.SQL.Samples != 2 || a.SQL.Truncated != 1 || a.SQL.Files != 2 || a.SQL.Missing != 1 || a.SQL.Corrupt != 3 || !a.SQL.Locked {
		t.Fatalf("summary = %+v", a.SQL)
	}
	var c *bbSeriesGroupCover
	for i := range a.Groups {
		if a.Groups[i].Group == "sqlDmv" {
			c = &a.Groups[i]
		}
	}
	if c == nil || c.Read != 2 || c.Partial != 1 {
		t.Fatalf("cover = %+v", c)
	}
	// Without a SQL read the answer carries no SQL part and no SQL cover: older answers keep their shape.
	b := buildBBSeriesShape(blackbox.Manifest{IntervalSeconds: 15}, nil, blackbox.ReadReport{}, blackbox.StateInfo{}, sqlWin(), 30, false, false)
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), `"sql"`) || strings.Contains(string(raw), "sqlDmv") {
		t.Fatalf("an answer with no SQL read mentions SQL: %s", raw)
	}
}

func TestSQLSeriesHostLinesWinTheKeyBudget(t *testing.T) {
	var s []blackbox.SQLSample
	for i := 0; i < bbSeriesMaxKeys+50; i++ {
		s = append(s, blackbox.SQLSample{T: at(1), Kind: "semaphores", Semaphores: []bbsql.Semaphores{{PoolID: i32(int32(i)), ResourceSemaphoreID: 0, Grantees: 1}}})
	}
	host := []blackbox.Sample{{V: 1, Run: "r", Seq: 1, T: sqlSeriesT0, CPU: f64(10)}}
	a := buildBBSeriesWith(blackbox.Manifest{IntervalSeconds: 15}, host, blackbox.ReadReport{}, blackbox.StateInfo{}, sqlWin(), 30, false, false, &bbSQLRead{Samples: s})
	if lineOf(a, "cpu") == nil || !a.Truncated || len(a.Lines) > bbSeriesMaxKeys {
		t.Fatalf("lines %d truncated %v", len(a.Lines), a.Truncated)
	}
}

/* ------------------------------------------------- through the real route */

func TestBlackboxSeriesRouteAddsTheSQLLines(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-sql", func(r *simRec) { r.SQL = true }))
	it := b.mustAdd(share, "")
	win := `{"start":"` + invT0.Add(-30*time.Minute).Format(time.RFC3339) + `","end":"` + invT0.Add(3*time.Hour).Format(time.RFC3339) + `"}`
	res := b.post("/api/blackbox/"+it.ID+"/series", win)
	if res.Code != http.StatusOK {
		t.Fatalf("series = %d %s", res.Code, res.Body)
	}
	var a bbSeriesAnswer
	decodeBody(t, res, &a)
	for _, k := range []string{
		"sqlDmv.requests.tasks", "sqlDmv.requests.blocked", "sqlDmv.requests.headBlockers", "sqlDmv.waits.PAGEIOLATCH_SH.deltaMs", "sqlDmv.schedulers.runnableTasks",
		"sqlDmv.semaphores.pool2sem0.waiters", "sqlDmv.tempdb.internalBytes", "sqlDmv.fileStats.readLatencyMs", "sqlDmv.fileStats.db5.readLatencyMs", "sqlDmv.ag.databases",
	} {
		if l := lineOf(a, k); l == nil || len(l.Points) == 0 {
			t.Errorf("no line %s; have %v", k, keysOf(a.Lines))
		}
	}
	// The busy stretch (10:20 to 11:00 on the test day) is where blocking shows; before it nothing is blocked.
	blocked := lineOf(a, "sqlDmv.requests.blocked")
	var busy, calm float64
	for _, p := range blocked.Points {
		ts := time.UnixMilli(int64(p[0])).UTC()
		switch {
		case !ts.Before(invT0.Add(80*time.Minute)) && ts.Before(invT0.Add(120*time.Minute)):
			busy = maxF(busy, p[3])
		case ts.Before(invT0.Add(78*time.Minute)) || !ts.Before(invT0.Add(122*time.Minute)): // a bucket on the edge holds both
			calm = maxF(calm, p[3])
		}
	}
	if busy < 4 || calm != 0 {
		t.Fatalf("blocked: busy %v calm %v", busy, calm)
	}
	// Calm minutes carry no LCK_M_X rows, so the line has points only where requests were in that wait.
	if a.SQL == nil || a.SQL.Samples == 0 || a.SQL.Locked || a.SQL.Corrupt != 0 {
		t.Fatalf("sql summary = %+v", a.SQL)
	}
	// Host lines are unchanged beside them.
	if lineOf(a, "cpu") == nil {
		t.Fatal("host lines went missing")
	}
	// No query text, login or name anywhere in the answer.
	raw := res.Body.String()
	for _, banned := range []string{"sql_text", "login", "program", "select ", "SELECT"} {
		if strings.Contains(raw, banned) {
			t.Errorf("answer mentions %q", banned)
		}
	}
}

func TestBlackboxSeriesRouteWithoutSQLServiceStaysAsBefore(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-nosql", nil))
	it := b.mustAdd(share, "")
	res := b.post("/api/blackbox/"+it.ID+"/series", `{"start":"`+invT0.Add(-30*time.Minute).Format(time.RFC3339)+`","end":"`+invT0.Add(3*time.Hour).Format(time.RFC3339)+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("series = %d %s", res.Code, res.Body)
	}
	var a bbSeriesAnswer
	decodeBody(t, res, &a)
	for _, l := range a.Lines {
		if strings.HasPrefix(l.Key, "sqlDmv") {
			t.Errorf("SQL line without a SQL service: %s", l.Key)
		}
	}
	// Every hour is simply missing: nothing to report, so the view says nothing.
	if a.SQL == nil || a.SQL.Samples != 0 || a.SQL.Files != 0 || a.SQL.Locked || a.SQL.Dropped != 0 || a.SQL.Missing == 0 {
		t.Fatalf("sql summary = %+v", a.SQL)
	}
}

func TestBlackboxSeriesRouteSaysWhenTheSQLFolderIsLocked(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-locked", func(r *simRec) { r.SQL, r.SQLLocked = true, true }))
	it := b.mustAdd(share, "")
	res := b.post("/api/blackbox/"+it.ID+"/series", `{"start":"`+invT0.Add(-30*time.Minute).Format(time.RFC3339)+`","end":"`+invT0.Add(3*time.Hour).Format(time.RFC3339)+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("series = %d %s", res.Code, res.Body)
	}
	var a bbSeriesAnswer
	decodeBody(t, res, &a)
	if a.SQL == nil || !a.SQL.Locked || a.SQL.Samples != 0 {
		t.Fatalf("sql summary = %+v", a.SQL)
	}
	if lineOf(a, "cpu") == nil {
		t.Fatal("a locked SQL folder must not take the host lines with it")
	}
}
