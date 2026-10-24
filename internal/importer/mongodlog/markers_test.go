package mongodlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// W2-l: start and stop markers. Synthetic 4.4+ structured lines only; the sentinel rides in
// a dropped attribute and must never reach an event.

const markerStart = "2001-09-09T01:00:03.000+00:00"
const markerStop = "2001-09-09T01:10:00.000+00:00"

func waitingLine(ts string) string {
	return logLineOf(ts, idWaitingForConnections, msgWaitingForConnections,
		`{"port":27017,"ssl":"off","note":"`+importertest.Sentinel+`"}`)
}

func shuttingDownLine(ts string) string {
	return logLineOf(ts, idShutdownStart, msgShutdownStart,
		`{"exitCode":0,"note":"`+importertest.Sentinel+`"}`)
}

func kindsOf(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// One start and one stop line in a synthetic 4.4+ log give one mongod-start and one
// mongod-stop point, on the host the file names, with the slow operation still emitted.
func TestStartAndStopMarkers(t *testing.T) {
	data := logData(
		startLine("mongo-a.example.test"),
		logLineOf("2001-09-09T01:00:00.010+00:00", idBuildInfo, "Build Info", `{"buildInfo":{"version":"7.0.2"}}`),
		waitingLine(markerStart),
		slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200),
		shuttingDownLine(markerStop),
	)
	evs, rep, err := parse(t, data, hostsOpts())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	starts := kindsOf(evs, KindMongodStart)
	stops := kindsOf(evs, KindMongodStop)
	slows := kindsOf(evs, KindSlowOp)
	if len(starts) != 1 || len(stops) != 1 || len(slows) != 1 || len(evs) != 3 {
		t.Fatalf("events: %d start, %d stop, %d slow, %d total", len(starts), len(stops), len(slows), len(evs))
	}
	st, sp := starts[0], stops[0]
	if !st.Start.Equal(st.End) || st.Start.Format("2006-01-02T15:04:05.000Z07:00") != "2001-09-09T01:00:03.000Z" {
		t.Errorf("start marker time: %v..%v", st.Start, st.End)
	}
	if !sp.Start.Equal(sp.End) || sp.Start.Format("2006-01-02T15:04:05.000Z07:00") != "2001-09-09T01:10:00.000Z" {
		t.Errorf("stop marker time: %v..%v", sp.Start, sp.End)
	}
	for _, e := range []evidence.Event{st, sp} {
		if e.Host != "mongo-a" || e.Source != Name || e.DurationMs != nil || len(e.Attrs) != 0 {
			t.Errorf("marker shape: host %q source %q duration %v attrs %v", e.Host, e.Source, e.DurationMs, e.Attrs)
		}
	}
	if rep.Emitted != 3 || rep.Quality.Sampling != "" {
		t.Errorf("report: emitted %d sampling %q", rep.Emitted, rep.Quality.Sampling)
	}
	assertNoSentinel(t, evs)
}

// A second listening socket logs the same line at the same time: one start marker. A later
// start (a restart in the same file) is a second marker.
func TestRepeatedStartLineIsOneMarkerPerTime(t *testing.T) {
	data := logData(
		startLine("mongo-a.example.test"),
		waitingLine(markerStart),
		waitingLine(markerStart),
		waitingLine("2001-09-09T01:20:00.000+00:00"),
	)
	evs, _, err := parse(t, data, hostsOpts())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(kindsOf(evs, KindMongodStart)); got != 2 {
		t.Fatalf("start markers: %d, want 2", got)
	}
}

// The text alone is enough: a different id with the same message still marks the start.
func TestMarkerMatchedByText(t *testing.T) {
	data := logData(
		startLine("mongo-a.example.test"),
		logLineOf(markerStart, 1, msgWaitingForConnections, `{"port":27017}`),
		logLineOf(markerStop, 1, msgShutdownStart, `{}`),
	)
	evs, _, err := parse(t, data, hostsOpts())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(kindsOf(evs, KindMongodStart)) != 1 || len(kindsOf(evs, KindMongodStop)) != 1 {
		t.Fatalf("events: %+v", evs)
	}
}

// Without a host label (no start-up line and no picked host) a marker emits nothing and the
// file is not refused on that account.
func TestMarkerWithoutHostEmitsNothing(t *testing.T) {
	data := logData(waitingLine(markerStart), shuttingDownLine(markerStop))
	evs, _, err := parse(t, data, hostsOpts())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("events without host: %d", len(evs))
	}
}

// The window filter applies to markers like any other event: a window that starts after the
// start marker keeps only the stop marker.
func TestMarkerWindow(t *testing.T) {
	o := opts()
	o.Window = importer.Window{From: time.Date(2001, 9, 9, 1, 5, 0, 0, time.UTC)}
	data := logData(startLine("mongo-a.example.test"), waitingLine(markerStart), shuttingDownLine(markerStop))
	evs, _, err := parse(t, data, o)
	if err != nil || len(kindsOf(evs, KindMongodStart)) != 0 || len(kindsOf(evs, KindMongodStop)) != 1 {
		t.Fatalf("windowed: %v %+v", err, evs)
	}
}

func assertNoSentinel(t *testing.T, evs []evidence.Event) {
	t.Helper()
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), importertest.Sentinel) {
			t.Fatalf("sentinel in event: %s", b)
		}
	}
}
