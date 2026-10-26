package calltrace

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

func fp(v float64) *float64 { return &v }

// callWith builds a finished call with a duration and the given parts
// (audit, api, other in ms; negative means absent).
func callWith(id string, dur, auditMs, apiMs, otherMs float64) Call {
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Duration(dur) * time.Millisecond)
	c := Call{ID: id, Start: start, End: &end, Parts: []Part{}}
	if dur > 0 {
		c.DurationMs = fp(dur)
	}
	if auditMs >= 0 {
		c.Parts = append(c.Parts, Part{Key: PartAudit, Label: "Audit write", Ms: auditMs})
	}
	if apiMs >= 0 {
		c.Parts = append(c.Parts, Part{Key: PartAPI, Label: "API work", Ms: apiMs})
	}
	if otherMs >= 0 {
		c.Parts = append(c.Parts, Part{Key: PartOther, Label: "Other time", Ms: otherMs})
	}
	return c
}

func byID(t *testing.T, cs []Check, id string) Check {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", id, cs)
	return Check{}
}

func lane(key, state string, events ...LaneEvent) Lane {
	return Lane{Key: key, Label: laneLabel(key), State: state, Sources: []string{}, Events: events}
}

func TestRunChecksAuditAndAPI(t *testing.T) {
	tests := []struct {
		name         string
		call         Call
		auditStatus  string
		apiStatus    string
		auditContain string
	}{
		{"audit most of the call", callWith("a", 2000, 1200, 100, -1), StatusConsistent, StatusContradicted, "1200 ms, 60% of the 2000 ms call"},
		{"audit large but under the floor", callWith("a", 1500, 900, 1500, -1), StatusNotEnoughData, StatusConsistent, "neither most of it"},
		{"audit small share", callWith("a", 2000, 100, 1700, -1), StatusContradicted, StatusConsistent, "5%"},
		{"audit middle share", callWith("a", 10000, 3000, 6000, -1), StatusNotEnoughData, StatusNotEnoughData, "neither most of it"},
		{"api exactly 80 percent", callWith("a", 1000, 50, 800, -1), StatusContradicted, StatusConsistent, ""},
		{"api exactly 50 percent", callWith("a", 1000, 400, 500, -1), StatusNotEnoughData, StatusNotEnoughData, ""},
		{"no parts", callWith("a", 1000, -1, -1, -1), StatusNotEnoughData, StatusNotEnoughData, "no audit time"},
		{"no duration", callWith("a", 0, 500, 500, -1), StatusNotEnoughData, StatusNotEnoughData, "no audit time"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := RunChecks(tt.call, nil)
			a, p := byID(t, cs, "audit-write-slow"), byID(t, cs, "api-work-dominates")
			if a.Status != tt.auditStatus {
				t.Errorf("audit status = %s, want %s (%s)", a.Status, tt.auditStatus, a.Detail)
			}
			if p.Status != tt.apiStatus {
				t.Errorf("api status = %s, want %s (%s)", p.Status, tt.apiStatus, p.Detail)
			}
			if !strings.Contains(a.Detail, tt.auditContain) {
				t.Errorf("audit detail %q lacks %q", a.Detail, tt.auditContain)
			}
			if a.Label != "Symptom" || len(a.CallIDs) != 1 || a.CallIDs[0] != "a" || a.Needs != "" {
				t.Errorf("bad envelope: %+v", a)
			}
		})
	}
}

func TestRunChecksLanes(t *testing.T) {
	d := fp(840)
	events := lane(LaneSQL, StateEvents,
		LaneEvent{ID: "1", DurationMs: fp(10)}, LaneEvent{ID: "2", DurationMs: d}, LaneEvent{ID: "3"})
	one := lane(LaneMongo, StateEvents, LaneEvent{ID: "1"})
	hidden := lane(LaneRedis, StateEvents, LaneEvent{ID: "1", DurationMs: fp(5)})
	hidden.Hidden = 4
	tests := []struct {
		name       string
		lane       Lane
		call       Call
		wantStatus string
		wantDetail string
		wantNeeds  string
	}{
		{"events", events, callWith("a", 1000, -1, -1, -1), StatusConsistent, "3 events from this source overlapped the call; the longest ran 840 ms.", ""},
		{"one event without duration", one, callWith("a", 1000, -1, -1, -1), StatusConsistent, "1 event from this source overlapped the call.", ""},
		{"hidden counted", hidden, callWith("a", 1000, -1, -1, -1), StatusConsistent, "5 events from this source overlapped the call; the longest ran 5 ms.", ""},
		{"none seen", lane(LaneSQL, StateNoneSeen), callWith("a", 1000, -1, -1, -1), StatusContradicted, "no event from this source overlapped it", ""},
		{"no data sql", lane(LaneSQL, StateNoData), callWith("a", 1000, -1, -1, -1), StatusNotTestable, "no file for this source", "a SQL Server log or export"},
		{"no data mongo", lane(LaneMongo, StateNoData), callWith("a", 1000, -1, -1, -1), StatusNotTestable, "no file", "a MongoDB log or export"},
		{"no data redis", lane(LaneRedis, StateNoData), callWith("a", 1000, -1, -1, -1), StatusNotTestable, "no file", "a Redis log or export"},
		{"no data iis", lane(LaneIIS, StateNoData), callWith("a", 1000, -1, -1, -1), StatusNotTestable, "no file", "an IIS log"},
		{"no case", lane(LaneIIS, StateNoCase), callWith("a", 1000, -1, -1, -1), StatusNotTestable, "No case is open", "an IIS log"},
		{"clock unchecked", lane(LaneSQL, StateClockUnchecked), callWith("a", 1000, -1, -1, -1), StatusNotEnoughData, "no checked time zone", ""},
		{"incomplete", lane(LaneSQL, StateIncomplete), callWith("a", 1000, -1, -1, -1), StatusNotEnoughData, "The read for this call hit its limit, so events may be missing.", ""},
		{"out of range", lane(LaneSQL, StateOutOfRange), callWith("a", 1000, -1, -1, -1), StatusNotEnoughData, "The files for this source do not cover this time.", ""},
		{"no end", events, func() Call { c := callWith("a", 1000, -1, -1, -1); c.End = nil; return c }(), StatusNotEnoughData, "no usable finish time", ""},
		{"clock anomaly", events, func() Call { c := callWith("a", 1000, -1, -1, -1); c.ClockAnomaly = true; return c }(), StatusNotEnoughData, "no usable finish time", ""},
		{"no duration", events, callWith("a", 0, -1, -1, -1), StatusNotEnoughData, "no usable finish time", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ck := byID(t, RunChecks(tt.call, []Lane{tt.lane}), tt.lane.Key+"-during-call")
			if ck.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s", ck.Status, tt.wantStatus)
			}
			if !strings.Contains(ck.Detail, tt.wantDetail) {
				t.Errorf("detail %q lacks %q", ck.Detail, tt.wantDetail)
			}
			if ck.Needs != tt.wantNeeds {
				t.Errorf("needs = %q, want %q", ck.Needs, tt.wantNeeds)
			}
			if want := tt.lane.Label + " was busy during this call"; ck.Title != want {
				t.Errorf("title = %q, want %q", ck.Title, want)
			}
		})
	}
}

func TestRunChecksLaneSelection(t *testing.T) {
	c := callWith("a", 1000, -1, -1, -1)
	cs := RunChecks(c, []Lane{lane(LaneIIS, StateNoneSeen), lane(LaneOther, StateEvents, LaneEvent{ID: "x"})})
	var ids []string
	for _, ck := range cs {
		ids = append(ids, ck.ID)
	}
	want := "audit-write-slow,api-work-dominates,iis-during-call"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("ids = %s, want %s", got, want)
	}
	if got := len(RunChecks(Call{}, nil)); got != 2 {
		t.Errorf("zero call, nil lanes: %d checks, want 2", got)
	}
}

func traceOf(id string, dur float64, parts [3]float64, lanes ...Lane) Trace {
	return Trace{Call: callWith(id, dur, parts[0], parts[1], parts[2]), Lanes: lanes}
}

func TestCompareChecks(t *testing.T) {
	none := [3]float64{-1, -1, -1}
	apiTop := [3]float64{100, 800, 100}
	auditTop := [3]float64{800, 100, 100}
	allLanes := func(state string) []Lane {
		return []Lane{lane(LaneSQL, state), lane(LaneMongo, state), lane(LaneRedis, state), lane(LaneIIS, state)}
	}
	ev := LaneEvent{ID: "e"}
	tests := []struct {
		name      string
		traces    []Trace
		id        string
		status    string
		detail    string
		needs     string
		callCount int
	}{
		{"same part 2", []Trace{traceOf("a", 1000, apiTop), traceOf("b", 1000, apiTop)}, "same-main-part", StatusConsistent, "The API work took the most time in all 2 calls.", "", 2},
		{"same part 3", []Trace{traceOf("a", 1000, auditTop), traceOf("b", 1000, auditTop), traceOf("c", 1000, auditTop)}, "same-main-part", StatusConsistent, "audit write", "", 3},
		{"different part", []Trace{traceOf("a", 1000, apiTop), traceOf("b", 1000, auditTop), traceOf("c", 1000, apiTop)}, "same-main-part", StatusContradicted, "call 1, API work, call 2, audit write, call 3, API work", "", 3},
		{"missing part", []Trace{traceOf("a", 1000, apiTop), traceOf("b", 1000, none)}, "same-main-part", StatusNotEnoughData, "no audit, API or other", "", 2},
		{"other part", []Trace{traceOf("a", 1000, [3]float64{1, 1, 500}), traceOf("b", 1000, [3]float64{1, 1, 500})}, "same-main-part", StatusConsistent, "other time", "", 2},

		{"lane all events 2", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateEvents, ev)), traceOf("b", 1000, none, lane(LaneSQL, StateEvents, ev))}, "sql-in-every-call", StatusConsistent, "Events from this source overlapped all 2 calls.", "", 2},
		{"lane all events 3", []Trace{traceOf("a", 1000, none, lane(LaneMongo, StateEvents, ev)), traceOf("b", 1000, none, lane(LaneMongo, StateEvents, ev)), traceOf("c", 1000, none, lane(LaneMongo, StateEvents, ev))}, "mongo-in-every-call", StatusConsistent, "all 3 calls", "", 3},
		{"lane some events", []Trace{traceOf("a", 1000, none, lane(LaneRedis, StateEvents, ev)), traceOf("b", 1000, none, lane(LaneRedis, StateNoneSeen)), traceOf("c", 1000, none, lane(LaneRedis, StateEvents, ev))}, "redis-in-every-call", StatusContradicted, "2 of 3 calls had an event from this source; the rest had none.", "", 3},
		{"lane none", []Trace{traceOf("a", 1000, none, lane(LaneIIS, StateNoneSeen)), traceOf("b", 1000, none, lane(LaneIIS, StateNoneSeen))}, "iis-in-every-call", StatusContradicted, "0 of 2 calls", "", 2},
		{"lane no data", []Trace{traceOf("a", 1000, none, lane(LaneIIS, StateNoData)), traceOf("b", 1000, none, lane(LaneIIS, StateEvents, ev))}, "iis-in-every-call", StatusNotTestable, "At least one call has no file for this source.", "an IIS log", 2},
		{"lane no case", []Trace{traceOf("a", 1000, none, allLanes(StateNoCase)...), traceOf("b", 1000, none, allLanes(StateNoCase)...)}, "sql-in-every-call", StatusNotTestable, "no file", "a SQL Server log or export", 2},
		{"lane missing counts as no data", []Trace{traceOf("a", 1000, none), traceOf("b", 1000, none)}, "mongo-in-every-call", StatusNotTestable, "no file", "a MongoDB log or export", 2},
		{"lane unchecked", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateClockUnchecked)), traceOf("b", 1000, none, lane(LaneSQL, StateEvents, ev))}, "sql-in-every-call", StatusNotEnoughData, "no checked time zone", "", 2},
		{"lane incomplete", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateIncomplete)), traceOf("b", 1000, none, lane(LaneSQL, StateEvents, ev))}, "sql-in-every-call", StatusNotEnoughData, "hit its limit", "", 2},
		{"lane out of range", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateOutOfRange)), traceOf("b", 1000, none, lane(LaneSQL, StateEvents, ev))}, "sql-in-every-call", StatusNotEnoughData, "do not cover this time", "", 2},
		{"unchecked wins over incomplete", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateIncomplete)), traceOf("b", 1000, none, lane(LaneSQL, StateClockUnchecked))}, "sql-in-every-call", StatusNotEnoughData, "no checked time zone", "", 2},
		{"no data wins over unchecked", []Trace{traceOf("a", 1000, none, lane(LaneSQL, StateClockUnchecked)), traceOf("b", 1000, none, lane(LaneSQL, StateNoData))}, "sql-in-every-call", StatusNotTestable, "no file", "a SQL Server log or export", 2},

		{"spread close", []Trace{traceOf("a", 1000, none), traceOf("b", 1500, none)}, "duration-spread", StatusConsistent, "1.5 times the fastest", "", 2},
		{"spread far", []Trace{traceOf("a", 1000, none), traceOf("b", 2400, none), traceOf("c", 3000, none)}, "duration-spread", StatusContradicted, "3.0 times", "", 3},
		{"spread middle", []Trace{traceOf("a", 1000, none), traceOf("b", 2400, none)}, "duration-spread", StatusNotEnoughData, "2.4 times", "", 2},
		{"spread unknown", []Trace{traceOf("a", 1000, none), traceOf("b", 0, none)}, "duration-spread", StatusNotEnoughData, "no duration", "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ck := byID(t, CompareChecks(tt.traces), tt.id)
			if ck.Status != tt.status {
				t.Errorf("status = %s, want %s (%s)", ck.Status, tt.status, ck.Detail)
			}
			if !strings.Contains(ck.Detail, tt.detail) {
				t.Errorf("detail %q lacks %q", ck.Detail, tt.detail)
			}
			if ck.Needs != tt.needs {
				t.Errorf("needs = %q, want %q", ck.Needs, tt.needs)
			}
			if len(ck.CallIDs) != tt.callCount || ck.CallIDs[0] != "a" || ck.Label != "Symptom" {
				t.Errorf("bad envelope: %+v", ck)
			}
		})
	}
}

func TestCompareChecksFewTraces(t *testing.T) {
	if got := CompareChecks(nil); got != nil {
		t.Errorf("nil input: %v", got)
	}
	if got := CompareChecks([]Trace{traceOf("a", 1000, [3]float64{1, 1, 1})}); got != nil {
		t.Errorf("one trace: %v", got)
	}
	if got := CompareChecks([]Trace{{}, {}}); len(got) != 6 {
		t.Errorf("zero traces: %d checks, want 6", len(got))
	}
}

func TestChecksPassCausalLint(t *testing.T) {
	ev := LaneEvent{ID: "e", DurationMs: fp(12)}
	states := []string{StateEvents, StateNoneSeen, StateNoData, StateClockUnchecked, StateNoCase, StateIncomplete, StateOutOfRange}
	var lanes []Lane
	for _, k := range []string{LaneSQL, LaneMongo, LaneRedis, LaneIIS} {
		for _, s := range states {
			l := lane(k, s)
			if s == StateEvents {
				l.Events = []LaneEvent{ev}
			}
			lanes = append(lanes, l)
		}
	}
	var all []Check
	calls := []Call{
		callWith("a", 2000, 1200, 100, -1), callWith("a", 2000, 100, 1700, -1),
		callWith("a", 10000, 3000, 6000, -1), callWith("a", 1000, -1, -1, -1), {},
	}
	for _, c := range calls {
		all = append(all, RunChecks(c, lanes)...)
	}
	for _, c := range calls {
		bad := c
		bad.End = nil
		all = append(all, RunChecks(bad, lanes)...)
	}
	var traces []Trace
	for _, s := range states {
		traces = append(traces, traceOf("a", 1000, [3]float64{100, 800, 100}, lane(LaneSQL, s, ev)))
		traces = append(traces, traceOf("b", 2500, [3]float64{800, 100, 100}, lane(LaneSQL, StateNoneSeen)))
		all = append(all, CompareChecks(traces[len(traces)-2:])...)
	}
	all = append(all, CompareChecks([]Trace{traceOf("a", 1000, [3]float64{1, 1, 1}, lanes...), traceOf("b", 1100, [3]float64{1, 1, 1}, lanes...)})...)
	if len(all) == 0 {
		t.Fatal("no checks produced")
	}
	for _, ck := range all {
		for _, s := range []string{ck.Title, ck.Detail, ck.Needs} {
			if err := evidence.LintCausal(s); err != nil {
				t.Errorf("check %s: %q: %v", ck.ID, s, err)
			}
		}
		if ck.Label != "Symptom" {
			t.Errorf("check %s label %q", ck.ID, ck.Label)
		}
		if (ck.Needs != "") != (ck.Status == StatusNotTestable) {
			t.Errorf("check %s: needs %q with status %s", ck.ID, ck.Needs, ck.Status)
		}
	}
}
