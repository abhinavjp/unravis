package mongodlog

import (
	"errors"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Batch-4 review fixes for the mongod.log importer (H1 helpers, L5, L6). Synthetic input only.

func startLine(host string) string {
	return logLineOf("2001-09-09T01:00:00.000+00:00", idStarting, "MongoDB starting", `{"host":"`+host+`"}`)
}

// L6: a host name inside the file never overrides the user's pick; one that the host map
// knows under another label refuses the file.
func TestObserveHostWithPick(t *testing.T) {
	body := logData(startLine("mongo-a.example.test"), slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200))

	o := hostsOpts() // map knows mongo-a.example.test as mongo-a
	o.Host = &importer.HostHint{Label: "mongo-a"}
	evs, rep, err := parse(t, body, o)
	if err != nil || len(evs) != 1 || evs[0].Host != "mongo-a" {
		t.Fatalf("same label: %v %+v", err, evs)
	}
	if len(rep.ObservedHosts) != 0 {
		t.Errorf("a picked host observes nothing: %v", rep.ObservedHosts)
	}

	o.Host = &importer.HostHint{Label: "mongo-b"}
	if _, _, err := parse(t, body, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("other label: got %v, want ErrUnknownHost", err)
	}

	// A name the map does not know leaves the pick in force.
	other := logData(startLine("other.example.test"), slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200))
	o.Host = &importer.HostHint{Label: "mongo-a"}
	if evs, _, err := parse(t, other, o); err != nil || len(evs) != 1 || evs[0].Host != "mongo-a" {
		t.Fatalf("unknown name with a pick: %v %+v", err, evs)
	}
}

func TestObserveHostWithoutPick(t *testing.T) {
	body := logData(startLine("mongo-a.example.test"), slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200))
	evs, rep, err := parse(t, body, hostsOpts())
	if err != nil || len(evs) != 1 || evs[0].Host != "mongo-a" || len(rep.ObservedHosts) != 1 {
		t.Fatalf("mapped: %v %+v %v", err, evs, rep.ObservedHosts)
	}
	unknown := logData(startLine("other.example.test"), slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200))
	if _, _, err := parse(t, unknown, hostsOpts()); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("unmapped name, no pick: %v", err)
	}
}

// L5: message 20698 is a restart marker; the text alone is one too.
func TestRestartMarkerIsRestartNotRotation(t *testing.T) {
	if idRestarted != 20698 {
		t.Fatalf("idRestarted = %d", idRestarted)
	}
	base := func(extra string) []byte {
		return logData(
			logLineOf("2001-09-09T01:00:00.000+00:00", idBuildInfo, "Build Info", `{"buildInfo":{"version":"7.0.2"}}`),
			logLineOf("2001-09-09T01:00:01.000+00:00", idOptions, "Options set by command line", `{"options":{"operationProfiling":{"slowOpThresholdMs":100}}}`),
			slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200), extra)
	}
	_, rep, err := parse(t, base(logLineOf("2001-09-09T01:10:00.000+00:00", idRestarted, "x", `{}`)), opts())
	if err != nil || rep.Quality.Sampling != samplingRestart {
		t.Fatalf("20698: %v sampling %q", err, rep.Quality.Sampling)
	}
}

func quality(mut func(*evidence.SourceQuality)) evidence.SourceQuality {
	f := time.Date(2001, 9, 9, 1, 0, 0, 0, time.UTC)
	l := f.Add(time.Hour)
	q := evidence.SourceQuality{SourceVersion: "7.0.2", Threshold: "operations slower than 100 ms",
		CoverageFirst: &f, CoverageLast: &l, TimezoneRule: clock.RuleOffsetInField}
	if mut != nil {
		mut(&q)
	}
	return q
}

// H1 helpers: the shared gap and threshold logic the timeline uses.
func TestAbsenceGapAndThreshold(t *testing.T) {
	iv := join2("2001-09-09T01:10:00Z", "2001-09-09T01:10:30Z")
	cases := []struct {
		name string
		mut  func(*evidence.SourceQuality)
		gap  string
	}{
		{"clean", nil, ""},
		{"no version", func(q *evidence.SourceQuality) { q.SourceVersion = "" }, "mongod version unknown: no Build Info line in the file"},
		{"no threshold", func(q *evidence.SourceQuality) { q.Threshold = "" }, "slowms unknown"},
		{"no offset rule", func(q *evidence.SourceQuality) { q.TimezoneRule = clock.RuleUTCField }, "timestamp offset rule not established"},
		{"sampling", func(q *evidence.SourceQuality) { q.Sampling = samplingRestart }, samplingRestart},
		{"no coverage", func(q *evidence.SourceQuality) { q.CoverageFirst, q.CoverageLast = nil, nil }, "the interval is not fully inside the log coverage"},
	}
	for _, tc := range cases {
		if got := AbsenceGap(quality(tc.mut), iv); got != tc.gap {
			t.Errorf("%s: gap %q, want %q", tc.name, got, tc.gap)
		}
	}
	late := join2("2001-09-09T01:50:00Z", "2001-09-09T02:10:00Z")
	if AbsenceGap(quality(nil), late) == "" {
		t.Error("an interval that runs past the coverage must be a gap")
	}
	for version, want := range map[string]string{
		"7.0.2":  "operations slower than 100 ms",
		"6.0.13": "operations slower than 100 ms",
		"8.0.1":  "operations whose working time (workingMillis) is over 100 ms",
		"9.1":    "operations whose working time (workingMillis) is over 100 ms",
		"":       "operations slower than 100 ms",
	} {
		if got := AbsenceThreshold(quality(func(q *evidence.SourceQuality) { q.SourceVersion = version })); got != want {
			t.Errorf("version %q: %q, want %q", version, got, want)
		}
	}
}
