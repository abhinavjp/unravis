package mongoexports

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func statMetric(evs []evidence.Event, line int64, name string) *evidence.Event {
	for i := range evs {
		if evs[i].Line == line && evs[i].Attrs["metric"] == name {
			return &evs[i]
		}
	}
	return nil
}

func nyOpts(date string) importer.Options {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	o := baseOpts()
	o.Host = hostA(loc)
	if date != "" {
		o.Supplied = map[string]string{"date": date}
	}
	return o
}

func statLine(tm string) string {
	return `{"mongo-a.example.test:27017":{"insert":"*0","query":"5","conn":"40","time":"` + tm + `"}}` + "\n"
}

func TestMongostatValues(t *testing.T) {
	evs, rep := mustParse(t, NewMongostat(), readFixture(t, "mongostat-good.jsonl"), statOpts())
	if rep.Rejected != 0 {
		t.Fatalf("rejected %d: %+v", rep.Rejected, rep.Errors)
	}
	check := func(line int64, name string, v float64, unit string) {
		t.Helper()
		e := statMetric(evs, line, name)
		if e == nil || *e.Value != v || e.Unit != unit || e.Kind != KindMetric || !e.Start.Equal(e.End) {
			t.Errorf("line %d %s: %+v, want %v %s", line, name, e, v, unit)
		}
	}
	// "*0" is a replicated count: the star goes, the number stays. "a|b" keeps the first.
	check(1, "insert", 0, "per-sec")
	check(1, "query", 12, "per-sec")
	check(1, "command", 20, "per-sec")
	check(1, "dirty", 0.4, "percent")
	check(1, "used", 61.2, "percent")
	check(1, "vsize", 4.5*1024*1024*1024, "bytes")
	check(1, "res", 2*1024*1024*1024, "bytes")
	check(1, "qr", 0, "count")
	check(1, "qw", 0, "count")
	check(1, "ar", 1, "count")
	check(1, "aw", 2, "count")
	check(1, "netIn", 1.2*1024, "per-sec")
	check(1, "netOut", 15*1024, "per-sec")
	check(1, "conn", 41, "count")
	// The second spelling of the network columns and a byte suffix.
	check(2, "netIn", 900, "per-sec")
	check(2, "netOut", 11*1024, "per-sec")
	check(2, "insert", 1, "per-sec")
	// Four spellings of the time: "Mon D HH:MM:SS", date-time, time of day, ISO with offset.
	for line, want := range map[int64]time.Time{1: ts(1, 0, 1), 2: ts(1, 0, 2), 3: ts(1, 0, 3), 4: ts(1, 0, 4)} {
		e := statMetric(evs, line, "conn")
		if e == nil || !e.End.Equal(want) {
			t.Errorf("line %d time: %+v, want %v", line, e, want)
		}
	}
	if e := statMetric(evs, 4, "conn"); e.TZRule != clock.RuleOffsetInField {
		t.Errorf("offset line rule %q", e.TZRule)
	}
	if e := statMetric(evs, 1, "conn"); e.TZRule != clock.RuleHostLocal {
		t.Errorf("host-local line rule %q", e.TZRule)
	}
	if !hasGap(rep, "host-local") || !hasGap(rep, "date is the one supplied") {
		t.Errorf("gaps: %v", rep.KnownGaps)
	}
	if statMetric(evs, 1, "locks") != nil {
		t.Error("a column outside the list was read")
	}
}

// A date supplied by the owner carries the times of day; at midnight it moves on one day.
func TestMongostatMidnightRollover(t *testing.T) {
	data := statLine("23:59:58") + statLine("23:59:59") + statLine("00:00:00") + statLine("00:00:01")
	o := nyOpts("2001-09-09")
	o.Host = hostA(time.UTC)
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	want := map[int64]time.Time{
		1: time.Date(2001, 9, 9, 23, 59, 58, 0, time.UTC), 2: time.Date(2001, 9, 9, 23, 59, 59, 0, time.UTC),
		3: time.Date(2001, 9, 10, 0, 0, 0, 0, time.UTC), 4: time.Date(2001, 9, 10, 0, 0, 1, 0, time.UTC),
	}
	for line, w := range want {
		if e := statMetric(evs, line, "conn"); e == nil || !e.End.Equal(w) {
			t.Errorf("line %d: %+v, want %v", line, e, w)
		}
	}
	if !hasGap(rep, "moved on by one day") {
		t.Errorf("no rollover gap: %v", rep.KnownGaps)
	}
	// A small step back (a duplicate or a reordered row) is not midnight.
	evs, rep = mustParse(t, NewMongostat(), []byte(statLine("10:00:05")+statLine("10:00:03")), statOpts())
	if e := statMetric(evs, 2, "conn"); e == nil || !e.End.Equal(time.Date(2001, 9, 9, 10, 0, 3, 0, time.UTC)) || hasGap(rep, "moved on") {
		t.Errorf("small step back: %+v %v", e, rep.KnownGaps)
	}
}

// Host-local times are placed with the host's zone. A time that happens twice or not at all
// is refused, never guessed.
func TestMongostatDSTAmbiguousAndNonexistent(t *testing.T) {
	o := nyOpts("2001-10-28") // US fall-back: 01:00-02:00 happens twice
	o.Limits.MaxRejectFraction = 1
	data := statLine("00:30:00") + statLine("01:30:00") + statLine("03:30:00")
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != importer.ReasonDSTAmbiguous || rep.Errors[0].Line != 2 {
		t.Fatalf("fold: rejected %d %+v", rep.Rejected, rep.Errors)
	}
	// 00:30 EDT is 04:30Z; 03:30 EST is 08:30Z.
	if e := statMetric(evs, 1, "conn"); e == nil || !e.End.Equal(time.Date(2001, 10, 28, 4, 30, 0, 0, time.UTC)) {
		t.Errorf("before the fold: %+v", e)
	}
	if e := statMetric(evs, 3, "conn"); e == nil || !e.End.Equal(time.Date(2001, 10, 28, 8, 30, 0, 0, time.UTC)) {
		t.Errorf("after the fold: %+v", e)
	}

	o = nyOpts("2001-04-01") // US spring-forward: 02:00-03:00 does not exist
	o.Limits.MaxRejectFraction = 1
	_, rep = mustParse(t, NewMongostat(), []byte(statLine("01:30:00")+statLine("02:30:00")+statLine("03:30:00")), o)
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonDSTNonexistent || rep.Errors[0].Line != 2 {
		t.Fatalf("gap: rejected %d %+v", rep.Rejected, rep.Errors)
	}
}

// A host-local time needs a zone: without one each such row is tz-unlabelled.
func TestMongostatNeedsZone(t *testing.T) {
	o := statOpts()
	o.Host = hostA(nil)
	o.Limits.MaxRejectFraction = 1
	data := statLine("01:00:01") + statLine("2001-09-09T01:00:02+00:00")
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonTZUnlabelled || len(evs) == 0 || evs[0].Line != 2 {
		t.Fatalf("rejected %d %+v", rep.Rejected, rep.Errors)
	}
}

// With no supplied date the "# unravis-sample" marker gives the day, converted to host time.
func TestMongostatSampleMarker(t *testing.T) {
	o := nyOpts("")
	data := "# unravis-sample 2001-09-09T05:00:30Z\n" + statLine("01:00:01") + statLine("01:00:02")
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	// 05:00:30Z is 01:00:30 EDT on the 9th: 01:00:01 EDT is 05:00:01Z.
	if e := statMetric(evs, 2, "conn"); e == nil || !e.End.Equal(time.Date(2001, 9, 9, 5, 0, 1, 0, time.UTC)) {
		t.Errorf("marker: %+v", e)
	}
	if !hasGap(rep, "sample marker") || hasGap(rep, "supplied by the owner") {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	// The marker's UTC day can be the day after the host's day: 02:00Z on the 10th is 22:00 EDT on the 9th.
	data = "# unravis-sample 2001-09-10T02:00:00Z\n" + statLine("21:59:59")
	evs, _ = mustParse(t, NewMongostat(), []byte(data), o)
	if e := statMetric(evs, 2, "conn"); e == nil || !e.End.Equal(time.Date(2001, 9, 10, 1, 59, 59, 0, time.UTC)) {
		t.Errorf("marker day: %+v", e)
	}
	// Neither a date nor a marker: the time of day cannot be placed, the row is bad-time.
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewMongostat(), []byte(statLine("2001-09-09T01:00:00+00:00")+statLine("01:00:01")), o)
	if err != nil || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || len(evs) == 0 {
		t.Fatalf("no date: %v rejected %d %+v", err, rep.Rejected, rep.Errors)
	}
	// A marker that is not a time is ignored.
	evs, rep, err = parse(t, NewMongostat(), []byte("# unravis-sample soon\n"+statLine("2001-09-09T01:00:00+00:00")+statLine("01:00:01")), o)
	if err != nil || rep.Rejected != 1 {
		t.Fatalf("bad marker: %v rejected %d", err, rep.Rejected)
	}
}

func TestMongostatOffsetOnlyRule(t *testing.T) {
	_, rep := mustParse(t, NewMongostat(), []byte(statLine("2001-09-09T01:00:00+00:00")+statLine("2001-09-09T01:00:01Z")), statOpts())
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField || hasGap(rep, "host-local") {
		t.Errorf("rule %q gaps %v", rep.Quality.TimezoneRule, rep.KnownGaps)
	}
}

// Several hosts in one document: each gets its own label and zone through the host map.
func TestMongostatManyHosts(t *testing.T) {
	o := baseOpts()
	o.Host = nil
	o.Hosts = func(name string) (importer.HostHint, bool) {
		switch name {
		case "mongo-a.example.test":
			return importer.HostHint{Label: "mongo-a", Role: "mongo", TZ: time.UTC}, true
		case "mongo-b.example.test":
			return importer.HostHint{Label: "mongo-b", Role: "mongo", TZ: time.UTC}, true
		}
		return importer.HostHint{}, false
	}
	line := `{"mongo-a.example.test:27017":{"conn":"4","query":"1","time":"2001-09-09T01:00:00Z"},` +
		`"mongo-b.example.test:27017":{"conn":"9","query":"2","time":"2001-09-09T01:00:00Z"}}` + "\n"
	evs, rep := mustParse(t, NewMongostat(), []byte(line), o)
	if len(evs) != 4 || len(rep.ObservedHosts) != 2 {
		t.Fatalf("%d events, observed %v", len(evs), rep.ObservedHosts)
	}
	labels := map[string]float64{}
	ids := map[string]bool{}
	for _, e := range evs {
		ids[e.EvidenceID] = true
		if e.Attrs["metric"] == "conn" {
			labels[e.Host] = *e.Value
		}
	}
	if labels["mongo-a"] != 4 || labels["mongo-b"] != 9 || len(ids) != 4 {
		t.Errorf("labels %v ids %d", labels, len(ids))
	}
	// A host the map does not know refuses the file.
	_, _, err := parse(t, NewMongostat(), []byte(strings.Replace(line, "mongo-b", "mongo-z", 1)), o)
	if err == nil {
		t.Error("an unmapped host was accepted")
	}
}

func TestMongostatRejects(t *testing.T) {
	cases := map[string]importer.Reason{
		`{"h":{"conn":"4","time":"25:00:00"}}`:        importer.ReasonBadTime,
		`{"h":{"conn":"4"}}`:                          importer.ReasonBadTime,
		`{"h":{"conn":"4","time":"Foo  9 01:00:00"}}`: importer.ReasonBadTime,
		`{"h":{"conn":"4","time":"Feb 31 01:00:00"}}`: importer.ReasonBadTime,
		`{"h":{"time":"01:00:00"}}`:                   importer.ReasonFieldCount,
		`{"h":{"conn":"x","time":"01:00:00"}}`:        importer.ReasonFieldCount,
		`{"h":{"conn":"-4","time":"01:00:00"}}`:       importer.ReasonFieldCount,
		`{"h":"text"}`:                                importer.ReasonFieldCount,
		`{}`:                                          importer.ReasonFieldCount,
		`[1,2]`:                                       importer.ReasonFieldCount,
	}
	good := statLine("01:00:00")
	for line, reason := range cases {
		o := statOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := parse(t, NewMongostat(), []byte(good+line+"\n"), o)
		if err != nil || rep.Rejected != 1 || rep.Errors[0].Reason != reason {
			t.Errorf("%s: want %s, got %v rejected %d %+v", line, reason, err, rep.Rejected, rep.Errors)
		}
		if len(evs) != 3 {
			t.Errorf("%s: %d events from the good row", line, len(evs))
		}
	}
}

// "Mon DD" rows carry no year; with an owner-supplied date the year comes from it and must move
// on when the month falls back from December to January.
func TestMongostatMonthDayYearRollover(t *testing.T) {
	data := statLine("Dec 31 23:59:58") + statLine("Dec 31 23:59:59") + statLine("Jan  1 00:00:00") + statLine("Jan  1 00:00:01")
	o := nyOpts("2001-12-31")
	o.Host = hostA(time.UTC)
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	want := map[int64]time.Time{
		1: time.Date(2001, 12, 31, 23, 59, 58, 0, time.UTC), 2: time.Date(2001, 12, 31, 23, 59, 59, 0, time.UTC),
		3: time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC), 4: time.Date(2002, 1, 1, 0, 0, 1, 0, time.UTC),
	}
	for line, w := range want {
		if e := statMetric(evs, line, "conn"); e == nil || !e.End.Equal(w) {
			t.Errorf("line %d: %+v, want %v", line, e, w)
		}
	}
	if !hasGap(rep, "month wrapped from December to January") {
		t.Errorf("no year rollover gap: %v", rep.KnownGaps)
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

// A small step back inside the same year (a duplicate or reordered row) keeps the year.
func TestMongostatMonthDaySmallStepBackKeepsYear(t *testing.T) {
	data := statLine("Dec 31 10:00:05") + statLine("Dec 30 10:00:03") + statLine("Nov 30 10:00:03")
	o := nyOpts("2001-12-31")
	o.Host = hostA(time.UTC)
	evs, rep := mustParse(t, NewMongostat(), []byte(data), o)
	for line, w := range map[int64]time.Time{
		2: time.Date(2001, 12, 30, 10, 0, 3, 0, time.UTC), 3: time.Date(2001, 11, 30, 10, 0, 3, 0, time.UTC),
	} {
		if e := statMetric(evs, line, "conn"); e == nil || !e.End.Equal(w) {
			t.Errorf("line %d: %+v, want %v", line, e, w)
		}
	}
	if hasGap(rep, "December to January") {
		t.Errorf("year gap on a small step back: %v", rep.KnownGaps)
	}
}
