package mongoexports

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func ts(h, m int, sec float64) time.Time {
	return time.Date(2001, 9, 9, h, m, 0, 0, time.UTC).Add(time.Duration(sec * float64(time.Second)))
}

func wantAttrs(t *testing.T, ev evidence.Event, want map[string]string) {
	t.Helper()
	if len(ev.Attrs) != len(want) {
		t.Errorf("attrs %v, want %v", ev.Attrs, want)
		return
	}
	for k, v := range want {
		if ev.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q (all %v)", k, ev.Attrs[k], v, ev.Attrs)
		}
	}
}

func TestProfileValues(t *testing.T) {
	evs, rep := mustParse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), baseOpts())
	if len(evs) != 6 || rep.Rows != 6 || rep.Emitted != 6 {
		t.Fatalf("%d events, report %+v", len(evs), rep)
	}
	e := evs[0]
	if e.Kind != KindSlowOp || e.Source != NameProfile || !e.End.Equal(ts(1, 0, 5.25)) || !e.Start.Equal(ts(1, 0, 5.13)) ||
		e.DurationMs == nil || *e.DurationMs != 120 || e.Unit != "ms" || e.TZRule != clock.RuleOffsetInField || e.Line != 1 {
		t.Errorf("event 0: %+v", e)
	}
	wantAttrs(t, e, map[string]string{"type": "query", "command": "find", "planSummary": "IXSCAN", "keysExamined": "10",
		"docsExamined": "10", "nreturned": "10", "numYields": "0", "queryHash": "1A2B3C4D"})
	wantAttrs(t, evs[1], map[string]string{"type": "command", "command": "aggregate", "planSummary": "COLLSCAN", "keysExamined": "0",
		"docsExamined": "48210", "nreturned": "3", "numYields": "377", "queryHash": "9F8E7D6C", "responseLength": "512", "workingMs": "2100"})
	// Extended JSON: $numberLong epoch milliseconds and a $numberInt duration; "numYields" spelling.
	if !evs[2].End.Equal(ts(1, 0, 12)) || *evs[2].DurationMs != 310 || evs[2].TZRule != clock.RuleUTCField {
		t.Errorf("event 2: %+v", evs[2])
	}
	if evs[2].Attrs["type"] != "update" || evs[2].Attrs["command"] != "update" || evs[2].Attrs["planSummary"] != "IDHACK" {
		t.Errorf("event 2 attrs: %v", evs[2].Attrs)
	}
	// A bare ISO string with an offset, a fractional duration.
	if !evs[3].End.Equal(ts(1, 0, 15)) || *evs[3].DurationMs != 95.5 {
		t.Errorf("event 3: %+v", evs[3])
	}
	// $date as a plain epoch number; several plan stages are kept as stage names only.
	if !evs[4].End.Equal(ts(1, 0, 30)) || evs[4].Attrs["planSummary"] != "IXSCAN,FETCH" || evs[4].Attrs["command"] != "getMore" {
		t.Errorf("event 4: %+v", evs[4])
	}
	if rep.First.IsZero() || !rep.First.Equal(ts(1, 0, 5.25)) || !rep.Last.Equal(ts(1, 0, 40.125)) {
		t.Errorf("coverage %v - %v", rep.First, rep.Last)
	}
	if rep.Quality.CoverageFirst == nil || rep.Quality.TimezoneRule == "" {
		t.Errorf("quality %+v", rep.Quality)
	}
}

func TestProfileSlowmsSupplied(t *testing.T) {
	_, rep := mustParse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), baseOpts())
	if rep.Quality.Threshold != "" || !hasGap(rep, "slowms unknown") {
		t.Errorf("without slowms: %q, gaps %v", rep.Quality.Threshold, rep.KnownGaps)
	}
	o := baseOpts()
	o.Supplied = map[string]string{"slowms": "100"}
	_, rep = mustParse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), o)
	if rep.Quality.Threshold != "operations slower than 100 ms" || hasGap(rep, "slowms unknown") {
		t.Errorf("with slowms: %q, gaps %v", rep.Quality.Threshold, rep.KnownGaps)
	}
	// A value that is not a whole number of milliseconds is ignored.
	o.Supplied = map[string]string{"slowms": "fast"}
	_, rep = mustParse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), o)
	if rep.Quality.Threshold != "" || !hasGap(rep, "slowms unknown") {
		t.Errorf("bad slowms: %q", rep.Quality.Threshold)
	}
}

func hasGap(rep importer.Report, sub string) bool {
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, sub) {
			return true
		}
	}
	return false
}

// Formats marked "verify" stay lenient: extra keys, a missing optional key, unknown plan
// stages and unknown command names do not reject the row. The words are just not kept.
func TestProfileLenient(t *testing.T) {
	lines := []string{
		`{"op":"query","millis":5,"ts":{"$date":"2001-09-09T01:00:00Z"},"brandNewField":{"x":[1,2,3]},"locks":{"Global":{"acquireCount":{"r":3}}}}`,
		`{"op":"query","millis":5,"ts":{"$date":"2001-09-09T01:00:01Z"},"command":{"someFutureCommand":1},"planSummary":"FUTURE_STAGE { a: 1 }"}`,
		`{"type":"command","durationMillis":7,"ts":"2001-09-09T01:00:02Z","command":{"find":"c"},"planSummary":"COLLSCAN"}`,
		`{"op":"query","millis":5,"ts":{"$date":"2001-09-09T01:00:03.123456789Z"},"nreturned":{"$numberLong":"12"},"docsExamined":{"$numberDouble":"4.0"}}`,
	}
	evs, rep := mustParse(t, NewProfile(), []byte(strings.Join(lines, "\n")+"\n"), baseOpts())
	if len(evs) != 4 || rep.Rejected != 0 {
		t.Fatalf("%d events, %d rejected", len(evs), rep.Rejected)
	}
	wantAttrs(t, evs[0], map[string]string{"type": "query"})
	wantAttrs(t, evs[1], map[string]string{"type": "query"})
	wantAttrs(t, evs[2], map[string]string{"type": "command", "command": "find", "planSummary": "COLLSCAN"})
	wantAttrs(t, evs[3], map[string]string{"type": "query", "nreturned": "12", "docsExamined": "4"})
}

func TestProfileRejectsWithReasons(t *testing.T) {
	cases := map[string]importer.Reason{
		`{"op":"query","millis":5,"ts":"2001-09-09T01:00:00"}`:                                 importer.ReasonTZUnlabelled,
		`{"op":"query","millis":5,"ts":"yesterday"}`:                                           importer.ReasonBadTime,
		`{"op":"query","millis":5,"ts":"1970-01-01T00:00:00Z"}`:                                importer.ReasonBadTime,
		`{"op":"query","millis":5}`:                                                            importer.ReasonBadTime,
		`{"op":"query","millis":-1,"ts":"2001-09-09T01:00:00Z"}`:                               importer.ReasonBadNumber,
		`{"op":"query","millis":1e99,"ts":"2001-09-09T01:00:00Z"}`:                             importer.ReasonBadNumber,
		`{"op":"query","ts":"2001-09-09T01:00:00Z"}`:                                           importer.ReasonBadNumber,
		`{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z","nreturned":1.5}`:                importer.ReasonBadNumber,
		`{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z","nreturned":-3}`:                 importer.ReasonBadNumber,
		`{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z"} {"x":1}`:                        importer.ReasonFieldCount,
		`{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z"`:                                 importer.ReasonFieldCount,
		`{"op":"query","millis":{"$numberLong":"x"},"ts":"2001-09-09T01:00:00Z"}`:              importer.ReasonBadNumber,
		`{"op":"query","millis":5,"ts":{"$date":{"$numberLong":"99999999999999"}}}`:            importer.ReasonBadTime,
		`{"op":"query","millis":5,"ts":{"$date":"2001-09-09T01:00:00Z","extra":1}}`:            importer.ReasonBadTime,
		`{"op":"query","millis":5,"ts":{"$date":"2001-09-09T01:00:00Z"},"queryHash":"nothex"}`: "",
	}
	good := `{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z"}`
	for line, reason := range cases {
		o := baseOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := parse(t, NewProfile(), []byte(good+"\n"+line+"\n"), o)
		if err != nil {
			t.Errorf("%s: %v", line, err)
			continue
		}
		if reason == "" {
			if rep.Rejected != 0 || len(evs) != 2 {
				t.Errorf("%s: rejected %d", line, rep.Rejected)
			}
			if _, ok := evs[1].Attrs["queryHash"]; ok {
				t.Errorf("%s: a non-hex hash was kept", line)
			}
			continue
		}
		if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != reason || len(evs) != 1 {
			t.Errorf("%s: want %s, got rejected %d errors %+v", line, reason, rep.Rejected, rep.Errors)
		}
	}
}

func TestProfileRefusesOtherFormats(t *testing.T) {
	for _, in := range []string{"time,op\n1,2\n", "plain text\n", "\n\n", "# only a comment\n"} {
		evs, _, err := parse(t, NewProfile(), []byte(in), baseOpts())
		if err == nil || len(evs) != 0 {
			t.Errorf("%q: %d events, %v", in, len(evs), err)
		}
	}
}

func TestCurrentOpValues(t *testing.T) {
	evs, rep := mustParse(t, NewCurrentOp(), readFixture(t, "currentop-good.jsonl"), baseOpts())
	if len(evs) != 4 || rep.Rejected != 0 {
		t.Fatalf("%d events, %d rejected", len(evs), rep.Rejected)
	}
	e := evs[0]
	if e.Kind != KindInflightOp || !e.End.Equal(ts(1, 0, 10)) || !e.Start.Equal(ts(1, 0, 7.5)) || *e.DurationMs != 2500 {
		t.Errorf("event 0: %+v", e)
	}
	wantAttrs(t, e, map[string]string{"op": "query", "planSummary": "COLLSCAN", "waitingForLock": "false", "numYields": "3"})
	// secs_running is used when microsecs_running is absent.
	if *evs[1].DurationMs != 4000 || !evs[1].Start.Equal(ts(1, 0, 6)) {
		t.Errorf("event 1: %+v", evs[1])
	}
	wantAttrs(t, evs[1], map[string]string{"op": "update", "waitingForLock": "true", "numYields": "0"})
	// Extended JSON time and $numberLong microseconds.
	if !evs[2].End.Equal(ts(1, 0, 20)) || *evs[2].DurationMs != 15000 || evs[2].Attrs["planSummary"] != "IXSCAN,FETCH" {
		t.Errorf("event 2: %+v", evs[2])
	}
	if !evs[3].End.Equal(ts(1, 0, 40)) || *evs[3].DurationMs != 3200 {
		t.Errorf("event 3: %+v", evs[3])
	}
	for _, sub := range []string{"snapshots", "recipe"} {
		if !hasGap(rep, sub) {
			t.Errorf("no gap about %q: %v", sub, rep.KnownGaps)
		}
	}
}

func TestServerStatusValues(t *testing.T) {
	evs, rep := mustParse(t, NewServerStatus(), readFixture(t, "serverstatus-good.jsonl"), baseOpts())
	if rep.Rejected != 0 || rep.Quality.SourceVersion != "7.0.5" {
		t.Fatalf("report %+v", rep)
	}
	byMetric := func(line int64, name string) *evidence.Event {
		for i := range evs {
			if evs[i].Line == line && evs[i].Attrs["metric"] == name && evs[i].Kind == KindMetric {
				return &evs[i]
			}
		}
		return nil
	}
	n := 0
	for _, e := range evs {
		if e.Line == 1 {
			n++
		}
	}
	if n != len(serverStatusMetrics) {
		t.Errorf("line 1 gave %d events, want %d", n, len(serverStatusMetrics))
	}
	if e := byMetric(1, "uptime"); e == nil || *e.Value != 86400 || e.Unit != "count" || !e.End.Equal(ts(1, 0, 0)) || !e.Start.Equal(e.End) {
		t.Errorf("uptime: %+v", e)
	}
	// mem.* is MiB in serverStatus: stored as bytes.
	if e := byMetric(1, "mem.resident"); e == nil || *e.Value != 2048*1048576 || e.Unit != "bytes" {
		t.Errorf("mem.resident: %+v", e)
	}
	if e := byMetric(1, "wiredTiger.cache.maximum bytes configured"); e == nil || *e.Value != 4294967296 || e.Unit != "bytes" {
		t.Errorf("cache max: %+v", e)
	}
	if e := byMetric(2, "connections.current"); e == nil || *e.Value != 44 {
		t.Errorf("$numberInt connections.current: %+v", e)
	}
	// Line 3 has fewer paths: only those present are emitted.
	if e := byMetric(3, "wiredTiger.cache.maximum bytes configured"); e != nil {
		t.Errorf("absent path emitted: %+v", e)
	}
	if e := byMetric(3, "opcounters.command"); e == nil || *e.Value != 30 {
		t.Errorf("line 3 opcounters.command: %+v", e)
	}
	// The uptime fell from 86700 to 120 while the clock moved on: a restart, 120 s before 01:10.
	var starts []evidence.Event
	for _, e := range evs {
		if e.Kind == KindMongodStart {
			starts = append(starts, e)
		}
	}
	if len(starts) != 1 || !starts[0].Start.Equal(ts(1, 8, 0)) || !starts[0].End.Equal(ts(1, 8, 0)) || starts[0].Line != 3 {
		t.Fatalf("mongod-start events: %+v", starts)
	}
	if hasGap(rep, "version unknown") {
		t.Errorf("version gap despite a version: %v", rep.KnownGaps)
	}
}

func TestServerStatusNoRestartWithoutDrop(t *testing.T) {
	// Rising uptime: no start. A drop with the clock going backwards (a reordered file): no start.
	lines := []string{
		`{"localTime":"2001-09-09T01:00:00Z","uptime":100}`,
		`{"localTime":"2001-09-09T01:01:00Z","uptime":160}`,
		`{"localTime":"2001-09-09T00:59:00Z","uptime":10}`,
	}
	evs, rep := mustParse(t, NewServerStatus(), []byte(strings.Join(lines, "\n")+"\n"), baseOpts())
	for _, e := range evs {
		if e.Kind == KindMongodStart {
			t.Errorf("unexpected restart: %+v", e)
		}
	}
	if !hasGap(rep, "version unknown") {
		t.Errorf("no version gap: %v", rep.KnownGaps)
	}
}

func TestServerStatusRejects(t *testing.T) {
	cases := map[string]importer.Reason{
		`{"localTime":"2001-09-09T01:00:00Z","connections":{"current":-1}}`:     importer.ReasonBadNumber,
		`{"localTime":"2001-09-09T01:00:00Z","connections":{"current":"many"}}`: importer.ReasonBadNumber,
		`{"localTime":"2001-09-09T01:00:00Z","notes":"nothing here"}`:           importer.ReasonFieldCount,
		`{"localTime":"2001-09-09T01:00:00","uptime":5}`:                        importer.ReasonTZUnlabelled,
		`{"localTime":"last tuesday","uptime":5}`:                               importer.ReasonBadTime,
		`{"uptime":5}`: importer.ReasonBadTime,
	}
	good := `{"localTime":"2001-09-09T00:59:00Z","uptime":5}`
	for line, reason := range cases {
		o := baseOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := parse(t, NewServerStatus(), []byte(good+"\n"+line+"\n"), o)
		if err != nil || rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != reason || len(evs) != 1 {
			t.Errorf("%s: want %s, got %v rejected %d %+v", line, reason, err, rep.Rejected, rep.Errors)
		}
	}
}
