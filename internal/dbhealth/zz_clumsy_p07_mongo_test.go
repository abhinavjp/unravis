package dbhealth

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

func clumsyWording(t *testing.T, name, s string) {
	t.Helper()
	if err := evidence.LintCausal(s); err != nil {
		t.Errorf("%s: %q: %v", name, s, err)
	}
	if strings.Contains(s, "%") {
		t.Errorf("%s: %q holds a percent sign", name, s)
	}
}

func TestClumsyP07M11EmptyAndNil(t *testing.T) {
	for name, docs := range map[string][]map[string]any{
		"nil":   nil,
		"empty": {},
		"junk":  {nil, {}, {"ts": "yesterday"}, {"ts": 5, "ns": 7}, {"usedDisk": true}},
	} {
		for _, oldest := range []time.Time{{}, at(1, 0, 0)} {
			o := evalLaneProfiler(100, oldest, docs, false, mreq())
			if o.m11 == AnswerYes {
				t.Errorf("%s: M11 Yes from nothing", name)
			}
			clumsyWording(t, name, o.check.Summary)
		}
	}
	// nothing in the incident and the profiler covered it: that is a No only because the profiler was on; with an empty record it is not.
	o := evalLaneProfiler(100, time.Time{}, nil, false, mreq())
	if o.m11 != "" {
		t.Errorf("empty profile record answered M11 %q", o.m11)
	}
}

func TestClumsyP07M11OddDocs(t *testing.T) {
	// spill flags that are not booleans do not count; spills outside the audit collection, in the reference
	// window or outside both windows do not count either.
	var docs []map[string]any
	for i := 0; i < 25; i++ {
		docs = append(docs, profDocMs(at(19, 10, i), "AuditDb.Logs", "IXSCAN { ts: 1 }", 200, false))
		d := profDocMs(at(20, 10, i), "AuditDb.Logs", "IXSCAN { ts: 1 }", 900, false)
		switch i % 4 {
		case 0:
			d["usedDisk"] = "true"
		case 1:
			d["usedDisk"] = float64(1)
		case 2:
			d["usedDisk"] = nil
		}
		docs = append(docs, d)
	}
	for i := 0; i < 10; i++ {
		docs = append(docs,
			profDocMs(at(20, 10, i), "Other.Logs", "IXSCAN", 900, true),
			profDocMs(at(19, 10, i), "AuditDb.Logs", "IXSCAN", 200, true),
			profDocMs(at(25, 10, i), "AuditDb.Logs", "IXSCAN", 900, true),
			profDocMs(at(20, 11, i), "AuditDb.Logs", "IXSCAN", 900, true), // at the exact end of the incident or later
			profDocMs(at(20, 9, i), "AuditDb.Logs", "IXSCAN", 900, true),  // between the windows
		)
	}
	o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq())
	if o.m11 == AnswerYes {
		t.Fatalf("M11 Yes from spills that do not count: %s", o.check.Summary)
	}
	clumsyWording(t, "odd", o.check.Summary)
}

func TestClumsyP07M11OddTimes(t *testing.T) {
	// NaN and negative millis do not make a Yes on their own, and spills alone (flat time) never make a Yes.
	docs := m11Docs(25, 200, 200, 25)
	if o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq()); o.m11 == AnswerYes {
		t.Errorf("spills with flat time read Yes")
	}
	docs = m11Docs(25, 200, 900, 25)
	for i := range docs {
		docs[i]["millis"] = math.NaN()
	}
	if o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq()); o.m11 == AnswerYes {
		t.Errorf("NaN times read Yes")
	}
	docs = m11Docs(25, 200, 900, 25)
	for i := range docs {
		docs[i]["millis"] = "slow"
	}
	if o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq()); o.m11 == AnswerYes {
		t.Errorf("string times read Yes")
	}
	// the rise with fewer than the minimum per side is not read
	docs = m11Docs(5, 200, 900, 5)
	if o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq()); o.m11 == AnswerYes {
		t.Errorf("five per side read Yes")
	}
}

func TestClumsyP07M11HugeAndBadWindows(t *testing.T) {
	docs := m11Docs(4000, 200, 900, 4000)
	o := evalLaneProfiler(100, at(1, 0, 0), docs, false, mreq())
	if o.m11 != AnswerYes {
		t.Errorf("many spills: %q", o.m11)
	}
	clumsyWording(t, "huge", o.check.Summary)
	for name, w := range map[string]Window{
		"zero":     {},
		"inverted": {Start: laneInc.End, End: laneInc.Start},
		"empty":    {Start: laneInc.Start, End: laneInc.Start},
	} {
		req := mreq()
		req.Incident = w
		o := evalLaneProfiler(100, at(1, 0, 0), m11Docs(25, 200, 900, 5), false, req)
		if o.m11 != "" && o.m11 != AnswerNo {
			t.Errorf("%s incident: M11 %q", name, o.m11)
		}
		if o.m11 == AnswerNo {
			t.Errorf("%s incident: unreadable window answered No", name)
		}
		req = mreq()
		req.Reference = w
		evalLaneProfiler(100, at(1, 0, 0), m11Docs(25, 200, 900, 5), false, req) // must not panic
	}
}

func TestClumsyP07M11FullRunNoProfiler(t *testing.T) {
	// profiler off / empty record: no M11 answer through the whole lane
	m := newProf(time.Time{})
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	if v, ok := r.Lane.Answers["M11"]; ok {
		t.Errorf("M11 = %q with an empty profile", v)
	}
	// database not picked
	req := mreq()
	req.Database = ""
	r = RunMongoLane(context.Background(), newProf(at(1, 0, 0), m11Docs(25, 200, 900, 4)...), "Mongo", req, fixedNow)
	if v, ok := r.Lane.Answers["M11"]; ok {
		t.Errorf("M11 = %q with no database", v)
	}
}
