package server

import (
	"context"
	"math"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/investigate"
)

// Clumsy-user checks for the Phase 06 server readers: imported SQL and HTTPERR
// events, and the metric lane.

// twoStretchBody has a gap between the reference and the incident, so the
// readers query two stretches.
func twoStretchBody() investigationRequest {
	return investigationRequest{SourceIDs: []string{"demo-west"},
		Reference: investigationWindow{invT0, invT0.Add(time.Hour)},
		Incident:  investigationWindow{invT0.Add(2 * time.Hour), invT0.Add(3 * time.Hour)}}
}

func clumsyImports(names ...string) []casestore.ImportRecord {
	var out []casestore.ImportRecord
	for _, n := range names {
		out = append(out, stubImport(n, invT0, invT0.Add(4*time.Hour)))
	}
	return out
}

func clumsyBuild(t *testing.T, c casestore.Case, body investigationRequest) (investigate.Input, investigationRead) {
	t.Helper()
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, body, nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	return in, read
}

// One source at the cap is dropped with a warning; the other source still reads.
func TestClumsyEventsCapOnOneSourceOnly(t *testing.T) {
	httpRow := importedEvent(1, sourceHTTPErr, "httperr", invT0.Add(time.Hour+time.Minute), map[string]string{"reason": "Timer_ConnectionIdle"})
	c := &stubTraceCase{
		imports: clumsyImports(sourceSQLXE, sourceHTTPErr),
		events: func(q casestore.EventQuery) []evidence.Event {
			if q.Sources[0] == sourceSQLXE && q.Limit != 1 {
				return make([]evidence.Event, casestore.MaxEventQuery) // exactly at the cap
			}
			if q.Sources[0] == sourceHTTPErr {
				return []evidence.Event{httpRow}
			}
			return nil
		},
	}
	in, read := clumsyBuild(t, c, importedBody())
	if in.SQLPresent || len(in.SQLEvents) != 0 || len(read.Warnings) != 1 {
		t.Errorf("capped SQL used: present %v events %d warnings %v", in.SQLPresent, len(in.SQLEvents), read.Warnings)
	}
	if _, ok := in.Lanes["D2"]; ok {
		t.Errorf("D2 answered from a capped read: %v", in.Lanes)
	}
	if !in.HTTPErrPresent || len(in.HTTPErr) != 1 {
		t.Errorf("HTTPERR lost with the SQL cap: %v %d", in.HTTPErrPresent, len(in.HTTPErr))
	}
}

// A cap on the second stretch alone also drops the whole source, not half of it.
func TestClumsyEventsCapOnSecondStretch(t *testing.T) {
	c := &stubTraceCase{
		imports: clumsyImports(sourceHTTPErr, sourceSQLXE),
		events: func(q casestore.EventQuery) []evidence.Event {
			if q.Window.From.Equal(invT0.Add(2 * time.Hour)) {
				return make([]evidence.Event, casestore.MaxEventQuery)
			}
			return []evidence.Event{importedEvent(1, q.Sources[0], map[string]string{sourceHTTPErr: "httperr", sourceSQLXE: "deadlock"}[q.Sources[0]], invT0.Add(time.Minute), nil)}
		},
	}
	in, read := clumsyBuild(t, c, twoStretchBody())
	if in.HTTPErrPresent || len(in.HTTPErr) != 0 || in.SQLPresent || len(in.SQLEvents) != 0 {
		t.Errorf("half-read source used: http %v/%d sql %v/%d", in.HTTPErrPresent, len(in.HTTPErr), in.SQLPresent, len(in.SQLEvents))
	}
	if len(read.Warnings) != 2 {
		t.Errorf("warnings %v", read.Warnings)
	}
	// One row under the cap is fine.
	c.events = func(q casestore.EventQuery) []evidence.Event {
		if q.Limit == 1 {
			return nil
		}
		return make([]evidence.Event, casestore.MaxEventQuery-1)
	}
	in, _ = clumsyBuild(t, c, twoStretchBody())
	if !in.SQLPresent {
		t.Errorf("a read one under the cap was dropped")
	}
}

// Rows with nil Attrs, empty and repeated evidence ids, and rows the store
// returned twice across stretches.
func TestClumsyEventsOddRows(t *testing.T) {
	at := invT0.Add(time.Hour + time.Minute)
	dup := evidence.Event{EvidenceID: "same", Source: sourceHTTPErr, Kind: "httperr", Start: at, End: at}
	noID1 := evidence.Event{Source: sourceHTTPErr, Kind: "httperr", Start: at.Add(time.Second), End: at.Add(time.Second)}
	noID2 := noID1
	c := &stubTraceCase{
		imports: clumsyImports(sourceHTTPErr),
		events: func(q casestore.EventQuery) []evidence.Event {
			return []evidence.Event{dup, noID1, noID2}
		},
	}
	in, _ := clumsyBuild(t, c, twoStretchBody())
	if !in.HTTPErrPresent {
		t.Fatal("HTTPERR not present")
	}
	// "same" is read in both stretches and kept once; the id-less rows cannot be
	// told apart, so each stretch keeps its own.
	if n := len(in.HTTPErr); n != 5 {
		t.Errorf("rows %d, want 5 (one 'same' + two id-less per stretch)", n)
	}
	for _, r := range in.HTTPErr {
		if r.Reason != "" || r.Status != "" {
			t.Errorf("nil Attrs gave %+v", r)
		}
	}
}

// An unvalidated XE file beside a validated error log: only the log is read, and
// the unvalidated one is not probed either.
func TestClumsyEventsMixedValidation(t *testing.T) {
	xe := stubImport(sourceSQLXE, invT0, invT0.Add(4*time.Hour))
	xe.TZRule = ""
	login := importedEvent(1, sourceSQLErrLog, "login-failed", invT0.Add(time.Hour+time.Minute), nil)
	deadlock := importedEvent(2, sourceSQLXE, "deadlock", invT0.Add(time.Hour+time.Minute), nil)
	c := importedCase([]casestore.ImportRecord{xe, stubImport(sourceSQLErrLog, invT0, invT0.Add(4*time.Hour))}, login, deadlock)
	in, _ := clumsyBuild(t, c, importedBody())
	if !in.SQLPresent || len(in.SQLEvents) != 1 || in.SQLEvents[0].Kind != "login-failed" {
		t.Fatalf("events %+v", in.SQLEvents)
	}
	if in.SQLKinds["deadlock"] || in.SQLKinds["call"] || !in.SQLKinds["login-failed"] {
		t.Errorf("kinds %v", in.SQLKinds)
	}
	if v, ok := in.Lanes["D2"]; ok {
		t.Errorf("D2 = %v with no validated XE source", v)
	}
	for _, q := range c.queries {
		if q.Sources[0] == sourceSQLXE {
			t.Errorf("unvalidated XE queried: %+v", q)
		}
	}
}

// A case with no incident window, a reversed one, or no sources at all.
func TestClumsyEventsBadBodies(t *testing.T) {
	c := importedCase(clumsyImports(sourceSQLXE, sourceHTTPErr, sourceSQLErrLog),
		importedEvent(1, sourceSQLXE, "deadlock", invT0.Add(time.Minute), nil))
	var read investigationRead
	in := investigate.Input{}
	for name, body := range map[string]investigationRequest{
		"zero":     {},
		"reversed": {Incident: investigationWindow{invT0.Add(time.Hour), invT0}, Reference: investigationWindow{invT0.Add(time.Hour), invT0}},
	} {
		sources, _ := caseSources(context.Background(), c)
		if err := readImportedEvents(context.Background(), c, body, sources, &in, &read); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if v, ok := in.Lanes["D2"]; ok {
			t.Errorf("%s: D2 = %v", name, v)
		}
	}
	// nil sources and nil input maps.
	if err := readImportedEvents(context.Background(), c, importedBody(), nil, &investigate.Input{}, &read); err != nil {
		t.Error(err)
	}
}

// Memory grants that are not real counts must not answer D6 either way. NaN and
// Inf never reach the reader (the store refuses them at import); a negative one
// does.
func TestClumsyMetricLanesGarbageGrants(t *testing.T) {
	for name, v := range map[string]float64{"negative": -5, "huge": math.MaxFloat64} {
		t.Run(name, func(t *testing.T) {
			var evs []evidence.Event
			for i := 0; i < 30; i++ {
				evs = append(evs, grantSample(invT0.Add(time.Duration(i)*time.Minute), v))
			}
			c := memGrantsCase(t, evs)
			in := metricInput()
			var read investigationRead
			readMetricLanes(context.Background(), c, &in, &read)
			got := in.Lanes["D6"]
			if name == "huge" {
				if got != investigate.Yes { // a huge but real count is a real pile-up
					t.Errorf("huge: D6 = %v", got)
				}
				return
			}
			if got != "" {
				t.Errorf("%s grants gave D6 = %v", name, got)
			}
		})
	}
	// One bad sample makes the whole counter unread.
	evs := grantSamples(30, 0)
	evs[7] = grantSample(evs[7].Start, -1)
	c := memGrantsCase(t, evs)
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if v, ok := in.Lanes["D6"]; ok {
		t.Errorf("one negative sample among zeros: D6 = %v, want no answer", v)
	}
}

// Incident windows the metric reader cannot use; nil maps on the input.
func TestClumsyMetricLanesBadWindows(t *testing.T) {
	c := memGrantsCase(t, grantSamples(30, 12))
	for name, w := range map[string]investigate.Window{
		"zero":     {},
		"reversed": {Start: invT0.Add(time.Hour), End: invT0},
		"empty":    {Start: invT0, End: invT0},
		"far past": {Start: invT0.AddDate(-50, 0, 0), End: invT0.AddDate(-50, 0, 1)},
	} {
		in := investigate.Input{Incident: w}
		var read investigationRead
		readMetricLanes(context.Background(), c, &in, &read)
		if v, ok := in.Lanes["D6"]; ok {
			t.Errorf("%s window: D6 = %v", name, v)
		}
	}
}
