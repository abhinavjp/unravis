package calltrace

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
)

func i64p(v int64) *int64 { return &v }

var t0 = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func tp(t time.Time) *time.Time { return &t }

func TestLaneOf(t *testing.T) {
	tests := map[string]string{
		"sql-errorlog": LaneSQL, "sql-xe": LaneSQL,
		"mongo-log": LaneMongo, "mongod-log": LaneMongo, "mongostat-csv": LaneMongo,
		"redis-slowlog": LaneRedis, "iis-w3c": LaneIIS,
		"": LaneOther, "sql": LaneOther, "SQL-x": LaneOther, "app-log": LaneOther, "xiis-w3c": LaneOther,
	}
	for in, want := range tests {
		if got := LaneOf(in); got != want {
			t.Errorf("LaneOf(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCallFromRecord(t *testing.T) {
	base := func() audit.RequestRecord {
		return audit.RequestRecord{ID: "r1", SourceID: "s", Controller: "C", Action: "A", Outcome: "ok", InitiatedAt: tp(t0)}
	}
	tests := []struct {
		name      string
		mod       func(*audit.RequestRecord)
		wantOK    bool
		anomaly   bool
		hasEnd    bool
		dur       *float64
		partKeys  []string
		partMs    []float64
		partLabel []string
	}{
		{"nil start", func(r *audit.RequestRecord) { r.InitiatedAt = nil }, false, false, false, nil, nil, nil, nil},
		{"zero start", func(r *audit.RequestRecord) { r.InitiatedAt = tp(time.Time{}) }, false, false, false, nil, nil, nil, nil},
		{"empty id", func(r *audit.RequestRecord) { r.ID = "" }, false, false, false, nil, nil, nil, nil},
		{"no completion", func(r *audit.RequestRecord) {}, true, false, false, nil, []string{}, nil, nil},
		{"no completion with duration", func(r *audit.RequestRecord) { r.DurationMs = fp(250) }, true, false, false, fp(250), []string{}, nil, nil},
		{"completion gives duration", func(r *audit.RequestRecord) { r.CompletedAt = tp(t0.Add(1500 * time.Millisecond)) }, true, false, true, fp(1500), []string{}, nil, nil},
		{"stored duration wins", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(t0.Add(time.Second))
			r.DurationMs = fp(900)
		}, true, false, true, fp(900), []string{}, nil, nil},
		{"negative stored duration ignored", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(t0.Add(time.Second))
			r.DurationMs = fp(-5)
		}, true, false, true, fp(1000), []string{}, nil, nil},
		{"completed before start", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(t0.Add(-time.Second))
			r.DurationMs = fp(900)
		}, true, true, false, nil, []string{}, nil, nil},
		{"all parts", func(r *audit.RequestRecord) {
			r.DurationMs = fp(1000)
			r.AuditCreateTimeMs = fp(300)
			r.ApiTimeMs = fp(200)
			r.PayloadRecordTimeMs = fp(40)
		}, true, false, false, fp(1000), []string{PartAudit, PartAPI, PartOther, PartPayload}, []float64{300, 200, 500, 40}, []string{"Audit write", "API work", "Other time", "Payload record"}},
		{"audit only treats api as zero", func(r *audit.RequestRecord) {
			r.DurationMs = fp(1000)
			r.AuditCreateTimeMs = fp(300)
		}, true, false, false, fp(1000), []string{PartAudit, PartOther}, []float64{300, 700}, nil},
		{"other not positive", func(r *audit.RequestRecord) {
			r.DurationMs = fp(500)
			r.AuditCreateTimeMs = fp(300)
			r.ApiTimeMs = fp(200)
		}, true, false, false, fp(500), []string{PartAudit, PartAPI}, []float64{300, 200}, nil},
		{"no audit or api means no other", func(r *audit.RequestRecord) {
			r.DurationMs = fp(500)
			r.PayloadRecordTimeMs = fp(5)
		}, true, false, false, fp(500), []string{PartPayload}, []float64{5}, nil},
		{"negative parts dropped", func(r *audit.RequestRecord) {
			r.DurationMs = fp(500)
			r.AuditCreateTimeMs = fp(-1)
			r.ApiTimeMs = fp(100)
		}, true, false, false, fp(500), []string{PartAPI, PartOther}, []float64{100, 400}, nil},
		{"no duration no other", func(r *audit.RequestRecord) { r.ApiTimeMs = fp(100) }, true, false, false, nil, []string{PartAPI}, []float64{100}, nil},
		{"six hours is still drawn", func(r *audit.RequestRecord) { r.CompletedAt = tp(t0.Add(6 * time.Hour)) }, true, false, true, fp(6 * 3600 * 1000), []string{}, nil, nil},
		{"over six hours is a clock anomaly", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(t0.Add(6*time.Hour + time.Second))
			r.DurationMs = fp(900)
		}, true, true, false, nil, []string{}, nil, nil},
		{"finish in year 10000 is a clock anomaly", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
		}, true, true, false, nil, []string{}, nil, nil},
		{"start in year 10000", func(r *audit.RequestRecord) { r.InitiatedAt = tp(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)) }, false, false, false, nil, nil, nil, nil},
		{"start before year 1", func(r *audit.RequestRecord) { r.InitiatedAt = tp(time.Date(0, 6, 1, 0, 0, 0, 0, time.UTC)) }, false, false, false, nil, nil, nil, nil},
		{"infinite duration is unknown", func(r *audit.RequestRecord) {
			r.CompletedAt = tp(t0.Add(time.Second))
			r.DurationMs = fp(math.Inf(1))
		}, true, false, true, fp(1000), []string{}, nil, nil},
		{"huge and NaN parts dropped", func(r *audit.RequestRecord) {
			r.DurationMs = fp(500)
			r.AuditCreateTimeMs = fp(1e300)
			r.ApiTimeMs = fp(math.NaN())
			r.PayloadRecordTimeMs = fp(math.Inf(1))
		}, true, false, false, fp(500), []string{}, nil, nil},
		{"huge duration is unknown", func(r *audit.RequestRecord) { r.DurationMs = fp(1e300) }, true, false, false, nil, []string{}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base()
			tt.mod(&r)
			c, ok := CallFromRecord(r)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if c.ClockAnomaly != tt.anomaly || (c.End != nil) != tt.hasEnd {
				t.Errorf("anomaly=%v end=%v, want %v %v", c.ClockAnomaly, c.End, tt.anomaly, tt.hasEnd)
			}
			if (c.DurationMs == nil) != (tt.dur == nil) || (tt.dur != nil && *c.DurationMs != *tt.dur) {
				t.Errorf("duration = %v, want %v", c.DurationMs, tt.dur)
			}
			if c.Parts == nil {
				t.Fatal("Parts is nil")
			}
			var keys []string
			var vals []float64
			for i, p := range c.Parts {
				keys = append(keys, p.Key)
				vals = append(vals, p.Ms)
				if tt.partLabel != nil && p.Label != tt.partLabel[i] {
					t.Errorf("label %d = %q", i, p.Label)
				}
			}
			if len(keys) != len(tt.partKeys) || (len(keys) > 0 && !reflect.DeepEqual(keys, tt.partKeys)) {
				t.Errorf("part keys = %v, want %v", keys, tt.partKeys)
			}
			if tt.partMs != nil && !reflect.DeepEqual(vals, tt.partMs) {
				t.Errorf("part ms = %v, want %v", vals, tt.partMs)
			}
			if c.ID != "r1" || c.SourceID != "s" || c.Controller != "C" || c.Action != "A" || c.Outcome != "ok" {
				t.Errorf("fields not copied: %+v", c)
			}
		})
	}
}

func TestCallFromRecordCopiesAndUTC(t *testing.T) {
	zone := time.FixedZone("x", 5*3600)
	start := t0.In(zone)
	dur := 100.0
	r := audit.RequestRecord{ID: "r", InitiatedAt: &start, CompletedAt: tp(t0.Add(time.Second).In(zone)), DurationMs: &dur}
	c, _ := CallFromRecord(r)
	if c.Start.Location() != time.UTC || c.End.Location() != time.UTC || !c.Start.Equal(t0) {
		t.Errorf("times not UTC: %v %v", c.Start, c.End)
	}
	dur = 1
	if *c.DurationMs != 100 {
		t.Error("duration aliases the record")
	}
}

func TestEventLabel(t *testing.T) {
	long := strings.Repeat("abcdefghij-", 10)
	tests := []struct {
		name string
		e    evidence.Event
		want string
	}{
		{"empty", evidence.Event{}, "event"},
		{"slow-op", evidence.Event{Kind: "slow-op"}, "slow operation"},
		{"slow-command", evidence.Event{Kind: "slow-command"}, "slow command"},
		{"inflight-op", evidence.Event{Kind: "inflight-op"}, "running operation"},
		{"app-request", evidence.Event{Kind: "app-request"}, "slow request"},
		{"heartbeat-late", evidence.Event{Kind: "heartbeat-late"}, "late heartbeat"},
		{"proxy-command", evidence.Event{Kind: "proxy-command"}, "slow proxy command"},
		{"process-gone", evidence.Event{Kind: "process-gone"}, "process exit window"},
		{"app-session", evidence.Event{Kind: "app-session"}, "session marker"},
		{"request", evidence.Event{Kind: "request"}, "web request"},
		{"httperr", evidence.Event{Kind: "httperr"}, "HTTP.sys error"},
		{"wait lock", evidence.Event{Kind: "wait", Attrs: map[string]string{"wait_class": "lock"}}, "lock wait"},
		{"wait latch", evidence.Event{Kind: "wait", Attrs: map[string]string{"wait_class": "latch"}}, "latch wait"},
		{"wait preemptive", evidence.Event{Kind: "wait", Attrs: map[string]string{"wait_class": "preemptive"}}, "preemptive wait"},
		{"wait other class", evidence.Event{Kind: "wait", Attrs: map[string]string{"wait_class": "secret value"}}, "wait"},
		{"wait no attrs", evidence.Event{Kind: "wait"}, "wait"},
		{"unlisted kind", evidence.Event{Kind: "apppool-recycle", Attrs: map[string]string{"x": "secret"}}, "event"},
		{"unlisted kind with causal words", evidence.Event{Kind: "caused-by-lock-xyz"}, "event"},
		{"long unlisted kind", evidence.Event{Kind: long}, "event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EventLabel(tt.e)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if len(got) > 40 {
				t.Errorf("label too long: %d", len(got))
			}
			if err := evidence.LintCausal(got); err != nil {
				t.Errorf("causal wording: %v", err)
			}
		})
	}
}

func ev(id, source string, start time.Time, durMs float64) evidence.Event {
	end := start.Add(time.Duration(durMs) * time.Millisecond)
	return evidence.Event{EvidenceID: id, Source: source, Kind: "slow-op", Start: start, End: end, DurationMs: fp(durMs), Severity: "warning"}
}

func laneByKey(t *testing.T, ls []Lane, key string) Lane {
	t.Helper()
	for _, l := range ls {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("lane %q missing", key)
	return Lane{}
}

func TestBuildLanesStates(t *testing.T) {
	win := Window{From: t0, To: t0.Add(10 * time.Second)}
	good := []SourceInfo{{Name: "sql-errorlog", Validated: true}, {Name: "redis-slowlog", Validated: true}}
	inside := ev("e1", "sql-errorlog", t0.Add(time.Second), 100)
	tests := []struct {
		name     string
		haveCase bool
		sources  []SourceInfo
		evs      []evidence.Event
		wantSQL  string
		wantRed  string
		sqlN     int
	}{
		{"no case", false, nil, nil, StateNoCase, StateNoCase, 0},
		{"no case ignores events", false, good, []evidence.Event{inside}, StateNoCase, StateNoCase, 0},
		{"no data", true, nil, []evidence.Event{inside}, StateNoData, StateNoData, 0},
		{"events and none seen", true, good, []evidence.Event{inside}, StateEvents, StateNoneSeen, 1},
		{"unvalidated source", true, []SourceInfo{{Name: "sql-errorlog", Validated: false}, {Name: "redis-slowlog", Validated: true}}, []evidence.Event{inside}, StateClockUnchecked, StateNoneSeen, 0},
		{"one of two sql sources unvalidated", true, []SourceInfo{{Name: "sql-errorlog", Validated: true}, {Name: "sql-xe", Validated: false}}, []evidence.Event{inside}, StateClockUnchecked, StateNoData, 0},
		{"duplicate source names merge", true, []SourceInfo{{Name: "sql-errorlog", Validated: true}, {Name: "sql-errorlog", Validated: false}}, nil, StateClockUnchecked, StateNoData, 0},
		{"event from source not in case", true, []SourceInfo{{Name: "redis-slowlog", Validated: true}}, []evidence.Event{inside}, StateNoData, StateNoneSeen, 0},
		{"event lane differs from source lane", true, good, []evidence.Event{ev("e2", "redis-slowlog", t0.Add(time.Second), 5)}, StateNoneSeen, StateEvents, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := BuildLanes(win, tt.haveCase, tt.sources, tt.evs)
			var keys []string
			for _, l := range ls {
				keys = append(keys, l.Key)
				if l.Sources == nil || l.Events == nil {
					t.Errorf("lane %s has nil slice", l.Key)
				}
			}
			if want := []string{LaneSQL, LaneMongo, LaneRedis, LaneIIS}; !reflect.DeepEqual(keys, want) {
				t.Fatalf("keys = %v, want %v", keys, want)
			}
			sql, red := laneByKey(t, ls, LaneSQL), laneByKey(t, ls, LaneRedis)
			if sql.State != tt.wantSQL || red.State != tt.wantRed {
				t.Errorf("states = %s/%s, want %s/%s", sql.State, red.State, tt.wantSQL, tt.wantRed)
			}
			if len(sql.Events) != tt.sqlN {
				t.Errorf("sql events = %d, want %d", len(sql.Events), tt.sqlN)
			}
			for i, label := range []string{"SQL Server", "MongoDB", "Redis", "IIS"} {
				if ls[i].Label != label {
					t.Errorf("label %d = %q", i, ls[i].Label)
				}
			}
		})
	}
}

func TestBuildLanesWindowTolerance(t *testing.T) {
	win := Window{From: t0, To: t0.Add(10 * time.Second)}
	src := []SourceInfo{{Name: "iis-w3c", Validated: true}}
	tests := []struct {
		name string
		e    evidence.Event
		want bool
	}{
		{"inside", ev("a", "iis-w3c", t0.Add(2*time.Second), 10), true},
		{"ends exactly 1s before", ev("a", "iis-w3c", t0.Add(-2*time.Second), 1000), true},
		{"ends just over 1s before", ev("a", "iis-w3c", t0.Add(-3*time.Second), 1999), false},
		{"point exactly 1s before", ev("a", "iis-w3c", t0.Add(-time.Second), 0), true},
		{"point over 1s before", ev("a", "iis-w3c", t0.Add(-1001*time.Millisecond), 0), false},
		{"starts exactly 1s after", ev("a", "iis-w3c", t0.Add(11*time.Second), 5), true},
		{"starts over 1s after", ev("a", "iis-w3c", t0.Add(11*time.Second+time.Millisecond), 5), false},
		{"spans the window", ev("a", "iis-w3c", t0.Add(-time.Minute), 180000), true},
		{"end before start skipped", evidence.Event{EvidenceID: "a", Source: "iis-w3c", Start: t0.Add(time.Second), End: t0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := laneByKey(t, BuildLanes(win, true, src, []evidence.Event{tt.e}), LaneIIS)
			if got := l.State == StateEvents; got != tt.want {
				t.Errorf("state = %s, want events=%v", l.State, tt.want)
			}
		})
	}
}

func TestBuildLanesCap(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Hour)}
	src := []SourceInfo{{Name: "mongo-log", Validated: true}}
	var evs []evidence.Event
	// 80 events; durations 1..80 ms, shuffled start order via index math.
	for i := 0; i < 80; i++ {
		evs = append(evs, ev(fmt.Sprintf("e%02d", i), "mongo-log", t0.Add(time.Duration((i*37)%80)*time.Second), float64(i+1)))
	}
	before := append([]evidence.Event(nil), evs...)
	l := laneByKey(t, BuildLanes(win, true, src, evs), LaneMongo)
	if len(l.Events) != MaxLaneEvents || l.Hidden != 20 {
		t.Fatalf("events=%d hidden=%d", len(l.Events), l.Hidden)
	}
	for i, e := range l.Events {
		if *e.DurationMs < 21 {
			t.Errorf("kept a short event: %v", *e.DurationMs)
		}
		if i > 0 && e.Start.Before(l.Events[i-1].Start) {
			t.Errorf("not sorted by start at %d", i)
		}
	}
	if !reflect.DeepEqual(evs, before) {
		t.Error("evs was mutated")
	}
}

func TestBuildLanesCapTies(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Hour)}
	src := []SourceInfo{{Name: "redis-slowlog", Validated: true}}
	var evs []evidence.Event
	for i := 0; i < 62; i++ { // all point events: ties broken by start, then id
		e := ev(fmt.Sprintf("p%02d", 61-i), "redis-slowlog", t0.Add(time.Duration(i/2)*time.Second), 0)
		evs = append(evs, e)
	}
	l := laneByKey(t, BuildLanes(win, true, src, evs), LaneRedis)
	if len(l.Events) != 60 || l.Hidden != 2 {
		t.Fatalf("events=%d hidden=%d", len(l.Events), l.Hidden)
	}
	for _, e := range l.Events {
		if e.Start.Equal(t0.Add(30 * time.Second)) {
			t.Errorf("latest-start event %s kept over earlier ones", e.ID)
		}
	}
}

func TestBuildLanesOtherLane(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	e := ev("o1", "app-log", t0.Add(time.Second), 10)
	e.Kind = "app-request"
	without := BuildLanes(win, true, []SourceInfo{{Name: "sql-errorlog", Validated: true}}, []evidence.Event{e})
	if len(without) != 4 {
		t.Fatalf("lanes without other source: %d", len(without))
	}
	with := BuildLanes(win, true, []SourceInfo{{Name: "sql-errorlog", Validated: true}, {Name: "app-log", Validated: true}}, []evidence.Event{e})
	if len(with) != 5 || with[4].Key != LaneOther || with[4].Label != "Other sources" {
		t.Fatalf("other lane missing: %+v", with)
	}
	o := with[4]
	if o.State != StateEvents || len(o.Events) != 1 || o.Events[0].Label != "slow request" || o.Events[0].ID != "o1" || o.Events[0].Severity != "warning" {
		t.Errorf("other lane = %+v", o)
	}
	if !reflect.DeepEqual(o.Sources, []string{"app-log"}) {
		t.Errorf("sources = %v", o.Sources)
	}
}

func TestBuildLanesSourcesSortedAndCopies(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	src := []SourceInfo{{Name: "sql-xe", Validated: true}, {Name: "sql-errorlog", Validated: true}}
	e := ev("e1", "sql-xe", t0.Add(time.Second), 10)
	ls := BuildLanes(win, true, src, []evidence.Event{e})
	sql := laneByKey(t, ls, LaneSQL)
	if !reflect.DeepEqual(sql.Sources, []string{"sql-errorlog", "sql-xe"}) {
		t.Errorf("sources = %v", sql.Sources)
	}
	if sql.Events[0].Start.Location() != time.UTC {
		t.Error("event time not UTC")
	}
	*sql.Events[0].DurationMs = 999
	if *e.DurationMs != 10 {
		t.Error("lane event aliases the source event")
	}
	for _, l := range ls {
		if l.Key != LaneSQL && (len(l.Sources) != 0 || l.Sources == nil) {
			t.Errorf("lane %s sources = %#v", l.Key, l.Sources)
		}
	}
}

func TestBuildLanesOutOfRange(t *testing.T) {
	win := Window{From: t0, To: t0.Add(10 * time.Second)}
	span := func(from, to time.Duration) []SourceInfo {
		return []SourceInfo{{Name: "sql-errorlog", Validated: true, From: t0.Add(from), To: t0.Add(to)}}
	}
	tests := []struct {
		name string
		src  []SourceInfo
		evs  []evidence.Event
		want string
	}{
		{"files end before the window", span(-3*time.Hour, -time.Hour), nil, StateOutOfRange},
		{"files start after the window", span(time.Hour, 2*time.Hour), nil, StateOutOfRange},
		{"files overlap the window", span(-time.Hour, time.Hour), nil, StateNoneSeen},
		{"files touch the window end", span(-time.Hour, 0), nil, StateNoneSeen},
		{"unknown span is never out of range", []SourceInfo{{Name: "sql-errorlog", Validated: true}}, nil, StateNoneSeen},
		{"one source in range keeps the lane", append(span(-3*time.Hour, -time.Hour), SourceInfo{Name: "sql-xe", Validated: true, From: t0, To: t0.Add(time.Minute)}), nil, StateNoneSeen},
		{"one source without a span keeps the lane", append(span(-3*time.Hour, -time.Hour), SourceInfo{Name: "sql-xe", Validated: true}), nil, StateNoneSeen},
		{"events win over a stale span", span(-3*time.Hour, -time.Hour), []evidence.Event{ev("e1", "sql-errorlog", t0.Add(time.Second), 50)}, StateEvents},
		{"unchecked clock wins", []SourceInfo{{Name: "sql-errorlog", Validated: false, From: t0.Add(-3 * time.Hour), To: t0.Add(-time.Hour)}}, nil, StateClockUnchecked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := laneByKey(t, BuildLanes(win, true, tt.src, tt.evs), LaneSQL)
			if l.State != tt.want {
				t.Errorf("state = %s, want %s", l.State, tt.want)
			}
			if tt.want == StateOutOfRange && len(l.Events) != 0 {
				t.Errorf("out-of-range lane plotted %d events", len(l.Events))
			}
		})
	}
}

func TestMarkIncomplete(t *testing.T) {
	in := []Lane{
		{Key: LaneSQL, State: StateNoneSeen},
		{Key: LaneMongo, State: StateEvents, Events: []LaneEvent{{ID: "e"}}},
		{Key: LaneRedis, State: StateNoData},
		{Key: LaneIIS, State: StateOutOfRange},
	}
	out := MarkIncomplete(in)
	want := []string{StateIncomplete, StateEvents, StateNoData, StateOutOfRange}
	for i, l := range out {
		if l.State != want[i] {
			t.Errorf("lane %s = %s, want %s", l.Key, l.State, want[i])
		}
	}
	if in[0].State != StateNoneSeen {
		t.Error("MarkIncomplete changed its input")
	}
}

func TestBuildLanesSeverityAllowlist(t *testing.T) {
	win := Window{From: t0, To: t0.Add(10 * time.Second)}
	e := ev("e1", "sql-errorlog", t0.Add(time.Second), 50)
	e.Severity = "caused-by-the-lock"
	l := laneByKey(t, BuildLanes(win, true, []SourceInfo{{Name: "sql-errorlog", Validated: true}}, []evidence.Event{e}), LaneSQL)
	if len(l.Events) != 1 || l.Events[0].Severity != "" {
		t.Fatalf("severity = %+v", l.Events)
	}
	if l.Events[0].Label != "slow operation" {
		t.Errorf("label = %q", l.Events[0].Label)
	}
}

// TestBuildLanesSQLXEEvent proves a saved sql-xe event inside the window
// lands in the sql lane in state events, so the tracing route can reuse
// BuildLanes with no new lane code (Appendix P5, TR-02).
func TestBuildLanesSQLXEEvent(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	t.Run("event inside the window", func(t *testing.T) {
		e := ev("xe1", "sql-xe", t0.Add(10*time.Second), 250)
		lane := laneByKey(t, BuildLanes(win, true, []SourceInfo{{Name: "sql-xe", Validated: true}}, []evidence.Event{e}), LaneSQL)
		if lane.State != StateEvents {
			t.Errorf("state = %s, want %s", lane.State, StateEvents)
		}
		if len(lane.Events) != 1 {
			t.Fatalf("events = %d, want 1", len(lane.Events))
		}
		if lane.Events[0].ID != "xe1" {
			t.Errorf("id = %q, want %q", lane.Events[0].ID, "xe1")
		}
		if lane.Events[0].Source != "sql-xe" {
			t.Errorf("source = %q, want %q", lane.Events[0].Source, "sql-xe")
		}
		if lane.Events[0].Label != "slow operation" {
			t.Errorf("label = %q, want %q", lane.Events[0].Label, "slow operation")
		}
		if lane.Events[0].DurationMs == nil || *lane.Events[0].DurationMs != 250 {
			t.Errorf("durationMs = %v, want 250", lane.Events[0].DurationMs)
		}
		if lane.Hidden != 0 {
			t.Errorf("hidden = %d, want 0", lane.Hidden)
		}
		if want := []string{"sql-xe"}; !reflect.DeepEqual(lane.Sources, want) {
			t.Errorf("sources = %v, want %v", lane.Sources, want)
		}
	})
	t.Run("event outside the window", func(t *testing.T) {
		e := ev("xe1", "sql-xe", t0.Add(time.Hour), 250)
		lane := laneByKey(t, BuildLanes(win, true, []SourceInfo{{Name: "sql-xe", Validated: true}}, []evidence.Event{e}), LaneSQL)
		if lane.State != StateNoneSeen {
			t.Errorf("state = %s, want %s", lane.State, StateNoneSeen)
		}
		if len(lane.Events) != 0 {
			t.Errorf("events = %d, want 0", len(lane.Events))
		}
	})
}

// TestBuildLanesSourceRefs proves a lane's SourceRefs carry the case-source
// ids of its SourceInfo entries, de-duplicated and sorted, while Lane.Sources
// (importer names) is untouched; a lane with no ids gets no sourceRefs key.
func TestBuildLanesSourceRefs(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	src := []SourceInfo{
		{Name: "iis-w3c", Validated: true, SourceID: "cs_7", Label: "web-a"},
		{Name: "sql-xe", Validated: true, SourceID: "imp_3", Label: "xe import"},
		{Name: "redis-slowlog", Validated: true},
	}
	ls := BuildLanes(win, true, src, nil)

	iis := laneByKey(t, ls, LaneIIS)
	if want := []SourceRef{{SourceID: "cs_7", Label: "web-a"}}; !reflect.DeepEqual(iis.SourceRefs, want) {
		t.Errorf("iis sourceRefs = %+v, want %+v", iis.SourceRefs, want)
	}
	if want := []string{"iis-w3c"}; !reflect.DeepEqual(iis.Sources, want) {
		t.Errorf("iis sources = %v, want %v", iis.Sources, want)
	}

	sql := laneByKey(t, ls, LaneSQL)
	if want := []SourceRef{{SourceID: "imp_3", Label: "xe import"}}; !reflect.DeepEqual(sql.SourceRefs, want) {
		t.Errorf("sql sourceRefs = %+v, want %+v", sql.SourceRefs, want)
	}

	red := laneByKey(t, ls, LaneRedis)
	if red.SourceRefs != nil {
		t.Errorf("redis sourceRefs = %+v, want nil", red.SourceRefs)
	}
	b, err := json.Marshal(red)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sourceRefs") {
		t.Errorf("redis lane JSON has a sourceRefs key: %s", b)
	}
}

// TestBuildLanesEventSourceID proves a lane event only gets a SourceID when
// its importer name maps to exactly one case-source id; a name shared by two
// ids is ambiguous (evidence.Event carries no import id of its own).
func TestBuildLanesEventSourceID(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	t.Run("unique id", func(t *testing.T) {
		src := []SourceInfo{{Name: "sql-errorlog", Validated: true, SourceID: "cs_7"}}
		e := ev("e1", "sql-errorlog", t0.Add(time.Second), 10)
		l := laneByKey(t, BuildLanes(win, true, src, []evidence.Event{e}), LaneSQL)
		if len(l.Events) != 1 || l.Events[0].SourceID != "cs_7" {
			t.Fatalf("events = %+v", l.Events)
		}
	})
	t.Run("ambiguous name", func(t *testing.T) {
		src := []SourceInfo{
			{Name: "sql-errorlog", Validated: true, SourceID: "cs_7"},
			{Name: "sql-errorlog", Validated: true, SourceID: "cs_8"},
		}
		e := ev("e1", "sql-errorlog", t0.Add(time.Second), 10)
		l := laneByKey(t, BuildLanes(win, true, src, []evidence.Event{e}), LaneSQL)
		if len(l.Events) != 1 || l.Events[0].SourceID != "" {
			t.Fatalf("events = %+v", l.Events)
		}
	})
}

// TestLaneEventStatusAndWin32 covers statusClass and win32Code directly, then
// proves a kind other than "request"/"httperr" never gets StatusClass even
// when attrs["status"] is set.
func TestLaneEventStatusAndWin32(t *testing.T) {
	statusTests := []struct {
		status string
		want   string
	}{
		{"200", "2xx"}, {"302", "3xx"}, {"404", "4xx"}, {"503", "5xx"},
		{"100", ""}, {"20", ""}, {"2000", ""}, {"-", ""}, {"", ""},
	}
	for _, tt := range statusTests {
		if got := statusClass(map[string]string{"status": tt.status}); got != tt.want {
			t.Errorf("statusClass(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}

	win32Tests := []struct {
		win32 string
		want  *int64
	}{
		{"0", i64p(0)}, {"64", i64p(64)}, {"4294967295", i64p(4294967295)},
		{"4294967296", nil}, {"-1", nil}, {"x", nil}, {"", nil},
	}
	for _, tt := range win32Tests {
		got := win32Code(map[string]string{"win32": tt.win32})
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("win32Code(%q) = %v, want %v", tt.win32, got, tt.want)
		}
	}

	win := Window{From: t0, To: t0.Add(time.Minute)}
	e := ev("e1", "sql-xe", t0.Add(time.Second), 10)
	e.Attrs = map[string]string{"status": "500"}
	l := laneByKey(t, BuildLanes(win, true, []SourceInfo{{Name: "sql-xe", Validated: true}}, []evidence.Event{e}), LaneSQL)
	if len(l.Events) != 1 || l.Events[0].StatusClass != "" {
		t.Fatalf("non-request kind carried a status class: %+v", l.Events)
	}
}

// TestLaneEventsNeverCarryPrivateAttrs proves no lane event, once marshalled,
// carries an attribute other than status/win32 (TR-18: no new attribute
// leaves the server).
func TestLaneEventsNeverCarryPrivateAttrs(t *testing.T) {
	win := Window{From: t0, To: t0.Add(time.Minute)}
	e := ev("e1", "iis-w3c", t0.Add(time.Second), 10)
	e.Kind = "request"
	e.Attrs = map[string]string{
		"status":         "200",
		"client-ip":      "10.0.0.1",
		"referer":        "https://example.com/secret-path",
		"cookie:session": "abc123secret",
	}
	l := laneByKey(t, BuildLanes(win, true, []SourceInfo{{Name: "iis-w3c", Validated: true}}, []evidence.Event{e}), LaneIIS)
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"client-ip", "referer", "cookie:session", "10.0.0.1", "example.com/secret-path", "abc123secret"} {
		if strings.Contains(s, bad) {
			t.Errorf("lane JSON leaked %q: %s", bad, s)
		}
	}
}
