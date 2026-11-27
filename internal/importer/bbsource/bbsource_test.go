package bbsource

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// All files here are synthetic: 2001 dates and invented names.
const good = `{"v":1,"run":"r-one","seq":1,"t":"2001-09-09T01:46:40Z","cpu":12.5,"memAvail":4000000000,"memCommit":9000000000,"diskQueue":0.5,"diskMs":3.2,"diskFreePct":55}
{"v":1,"run":"r-one","seq":2,"t":"2001-09-09T01:46:55Z","cpu":80,"memAvail":3900000000,"procs":[{"pid":4,"name":"web-a.exe","threads":2}]}
{"v":1,"run":"r-one","seq":3,"t":"2001-09-09T01:47:10Z","cpu":91.25,"diskMs":14}
`

// goodGroups is good plus a sample with the groups, the fixture of the conformance run and the fuzz seed.
const goodGroups = good + `{"v":1,"run":"r-one","seq":4,"t":"2001-09-09T01:47:25Z","sqlHost":{"instances":[{"instance":"i","batchRequestsPerSec":5}]},"http":{"queues":[{"pool":"a","currentQueueSize":1}]},"iis":{"workers":[{"pool":"a","activeRequests":2}]},"tcp":{"retransmitsPerSec":0.5},"redis":{"state":"ok","numbers":{"used_memory":1000,"uptime_in_seconds":50,"used_cpu_user":3},"words":{"role":"master"},"slow":[{"id":1,"t":1000000100,"usec":5000,"cmd":"GET"}],"latency":[{"event":"fork","t":1000000090,"ms":12,"maxMs":30}]},"events":{"events":[{"channel":1,"eventId":7034,"level":2,"count":1,"t":"2001-09-09T01:47:20Z"}]},"sqlLog":{"errors":[{"instance":"i","kind":"error","number":50000,"severity":16,"count":1,"t":"2001-09-09T01:47:21Z"}]},"dumps":{"dumps":[{"name":"a.dmp","size":5,"mtime":"2001-09-09T01:47:22Z"}]}}
`

const bad = `{"v":1,"run":"r-one","seq":1,"t":"2001-09-09T01:46:40Z","cpu":12.5}
{"v":1,"run":"r-one","seq":2,"t":"SENTINEL-7f3a","cpu":12.5}
{"v":1,"run":"SENTINEL-7f3a","seq":3,"t":"2001-09-09T01:47:10Z","cpu":-4}
`

func fixtures() importertest.Fixtures {
	return importertest.Fixtures{
		Good: []byte(goodGroups), Bad: []byte(bad), NoFreeText: true,
		Options: importer.Options{Host: &importer.HostHint{Label: "web-a", Role: "iis"}},
	}
}

func TestConformance(t *testing.T) { importertest.Run(t, New(), fixtures()) }

func FuzzParse(f *testing.F) { importertest.FuzzParse(f, New(), fixtures()) }

func TestConstantsMatchTheRecorder(t *testing.T) {
	if SupportedFormat != blackbox.FormatVersion {
		t.Errorf("the reader knows format %d, the recorder writes %d", SupportedFormat, blackbox.FormatVersion)
	}
	if LateIntervals != blackbox.LateIntervals {
		t.Errorf("gap rule %d intervals, the recorder's rule is %d", LateIntervals, blackbox.LateIntervals)
	}
}

func parse(t *testing.T, data string, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	if opts.Host == nil {
		opts.Host = &importer.HostHint{Label: "web-a"}
	}
	opts.CaseID, opts.ImportNonce = "harnesscase00000000000000a", "n1"
	var evs []evidence.Event
	rep, err := New().Parse(context.Background(), strings.NewReader(data), opts, func(e evidence.Event) error {
		evs = append(evs, e)
		return nil
	})
	return evs, rep, err
}

func counters(evs []evidence.Event, name string) []float64 {
	var out []float64
	for _, e := range evs {
		if e.Kind == KindCounter && e.Attrs["counter"] == name && e.Value != nil {
			out = append(out, *e.Value)
		}
	}
	return out
}

func TestHostFiguresBecomeCounters(t *testing.T) {
	evs, rep, err := parse(t, good, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := counters(evs, "% Processor Time"); len(got) != 3 || got[0] != 12.5 || got[2] != 91.25 {
		t.Errorf("cpu %v", got)
	}
	if got := counters(evs, "Avg. Disk sec/Transfer"); len(got) != 2 || got[0] != 0.0032 || got[1] != 0.014 {
		t.Errorf("disk ms %v", got)
	}
	// Sample 2 holds no diskQueue: no event, and above all no 0.
	if got := counters(evs, "Current Disk Queue Length"); len(got) != 1 {
		t.Errorf("a missing figure must give no event, got %v", got)
	}
	for _, e := range evs {
		if e.Host != "web-a" || e.TZRule != "utc-field" || e.Source != Name {
			t.Fatalf("event %+v", e)
		}
		if strings.Contains(e.Attrs["object"], "web-a.exe") {
			t.Fatal("a process name reached an event")
		}
	}
	if rep.Rows != 3 || rep.Rejected != 0 || len(rep.ObservedHosts) != 0 {
		t.Errorf("report %+v", rep)
	}
	if rep.Quality.TimezoneRule != "utc-field" || rep.First.IsZero() || !rep.Last.After(rep.First) {
		t.Errorf("quality %+v", rep.Quality)
	}
}

func TestRecorderStartOnlyAtSampleOne(t *testing.T) {
	evs, _, err := parse(t, good, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, e := range evs {
		if e.Kind == KindRunStart {
			starts++
			if !e.Start.Equal(time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC)) || e.Severity != "info" {
				t.Errorf("start event %+v", e)
			}
		}
	}
	if starts != 1 {
		t.Errorf("%d start events, want 1", starts)
	}
	// A later hourly file of the same run starts at seq 40: it is not a start.
	later := `{"v":1,"run":"r-one","seq":40,"t":"2001-09-09T02:00:00Z","cpu":1}` + "\n" + `{"v":1,"run":"r-one","seq":41,"t":"2001-09-09T02:00:15Z","cpu":1}` + "\n"
	evs, _, err = parse(t, later, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == KindRunStart {
			t.Error("the first line of a later hourly file is not a recorder start")
		}
	}
}

func TestGapsAreSpansAfterThreeIntervals(t *testing.T) {
	var b strings.Builder
	at := time.Date(2001, 9, 9, 1, 0, 0, 0, time.UTC)
	seq := 1
	add := func(d time.Duration) {
		at = at.Add(d)
		b.WriteString(`{"v":1,"run":"r","seq":` + itoa(int64(seq)) + `,"t":"` + at.Format(time.RFC3339) + `","cpu":1}` + "\n")
		seq++
	}
	for i := 0; i < 6; i++ {
		add(15 * time.Second)
	}
	add(45 * time.Second) // exactly 3 intervals: not a gap
	add(46 * time.Second) // more than 3: a gap
	add(15 * time.Second)
	evs, rep, err := parse(t, b.String(), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var gaps []evidence.Event
	for _, e := range evs {
		if e.Kind == KindGap {
			gaps = append(gaps, e)
		}
	}
	if len(gaps) != 1 {
		t.Fatalf("%d gaps, want 1", len(gaps))
	}
	g := gaps[0]
	if g.DurationMs == nil || *g.DurationMs != 46000 || !g.End.Equal(g.Start.Add(46*time.Second)) || g.Severity != "warning" || g.Attrs["intervals"] != "3" {
		t.Errorf("gap %+v", g)
	}
	if !contains(rep.KnownGaps, GapIntervalGuess) {
		t.Errorf("the interval guess must be said: %v", rep.KnownGaps)
	}
}

// run builds a recording from spacings: each entry is the time since the previous sample, in seconds.
func run(id string, start time.Time, spacings ...int) (string, time.Time) {
	var b strings.Builder
	at := start
	for i, d := range spacings {
		at = at.Add(time.Duration(d) * time.Second)
		b.WriteString(`{"v":1,"run":"` + id + `","seq":` + itoa(int64(i+1)) + `,"t":"` + at.Format(time.RFC3339) + `","cpu":1}` + "\n")
	}
	return b.String(), at
}

func gapsOf(evs []evidence.Event) (n int) {
	for _, e := range evs {
		if e.Kind == KindGap {
			n++
		}
	}
	return n
}

func rep(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

var t0 = time.Date(2001, 9, 9, 1, 0, 0, 0, time.UTC)

func TestAWidenedCadenceIsNoGap(t *testing.T) {
	data, _ := run("r", t0, append(rep(40, 15), rep(20, 60)...)...)
	evs, _, err := parse(t, data, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := gapsOf(evs); n != 0 {
		t.Errorf("%d gaps in a recording whose cadence widened once", n)
	}
}

func TestAHoleAfterTwoSpacingsIsFound(t *testing.T) {
	data, _ := run("r", t0, 15, 15, 3*3600)
	evs, _, err := parse(t, data, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := gapsOf(evs); n != 1 {
		t.Errorf("%d gaps, want the 3 h hole", n)
	}
}

func TestARestartIsJudgedAtOneAndAHalfIntervals(t *testing.T) {
	a, end := run("a", t0, rep(10, 15)...)
	b, _ := run("b", end.Add(21*time.Second), rep(10, 15)...) // first sample of b is 36 s after the last of a
	evs, _, err := parse(t, a+b, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := gapsOf(evs); n != 1 {
		t.Errorf("%d gaps, want the 36 s restart", n)
	}
	b2, _ := run("b", end.Add(5*time.Second), rep(10, 15)...) // 20 s: a prompt restart
	evs, _, _ = parse(t, a+b2, importer.Options{})
	if n := gapsOf(evs); n != 0 {
		t.Errorf("%d gaps for a prompt restart", n)
	}
}

func TestASampleWithABadFigureStillCountsForGapsAndRunStart(t *testing.T) {
	var b strings.Builder
	at := t0
	for i := 0; i < 20; i++ {
		at = at.Add(15 * time.Second)
		cpu := "1"
		if i >= 8 && i <= 11 {
			cpu = "-1"
		}
		b.WriteString(`{"v":1,"run":"r","seq":` + itoa(int64(i+1)) + `,"t":"` + at.Format(time.RFC3339) + `","cpu":` + cpu + `}` + "\n")
	}
	lenient := importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}}
	evs, _, err := parse(t, b.String(), lenient)
	if err != nil {
		t.Fatal(err)
	}
	if n := gapsOf(evs); n != 0 {
		t.Errorf("%d false gaps where the recorder wrote samples", n)
	}
	one := `{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:46:40Z","cpu":500}` + "\n" + good2
	evs, _, _ = parse(t, one, lenient)
	starts := 0
	for _, e := range evs {
		if e.Kind == KindRunStart {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d run starts, want 1 although sample 1 had a bad figure", starts)
	}
}

const good2 = `{"v":1,"run":"r","seq":2,"t":"2001-09-09T01:46:55Z","cpu":1}` + "\n"

func TestGapCapAndEdgesAreSaid(t *testing.T) {
	sp := []int{15, 15, 15}
	for i := 0; i < maxGapEvents+5; i++ {
		sp = append(sp, 600, 15, 15, 15)
	}
	data, _ := run("r", t0, sp...)
	evs, rp, err := parse(t, data, importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if n := gapsOf(evs); n != maxGapEvents {
		t.Errorf("%d gaps, want the cap %d", n, maxGapEvents)
	}
	if !contains(rp.KnownGaps, GapTooMany) || !contains(rp.KnownGaps, GapFileEdges) {
		t.Errorf("known gaps %v", rp.KnownGaps)
	}
}

func TestFreeSpaceIsNotTakenForAnotherVolume(t *testing.T) {
	evs, _, err := parse(t, good, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Attrs["counter"] == "% Free Space" && e.Attrs["instance"] != VolumeInstance {
			t.Errorf("free space instance %q", e.Attrs["instance"])
		}
	}
}

func TestRestartFractionMatchesTheRecorder(t *testing.T) {
	if float64(RestartGapIntervalsNum)/float64(RestartGapIntervalsDen) != blackbox.RestartGapIntervals {
		t.Error("restart gap fraction differs from the recorder's")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestHostIsRequired(t *testing.T) {
	_, err := New().Parse(context.Background(), strings.NewReader(good), importer.Options{CaseID: "c", ImportNonce: "n"}, func(evidence.Event) error { return nil })
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("err %v", err)
	}
}

func TestNewerFormatIsRefusedNotGuessed(t *testing.T) {
	_, _, err := parse(t, `{"v":2,"run":"r","seq":1,"t":"2001-09-09T01:46:40Z","cpu":1}`+"\n", importer.Options{})
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat {
		t.Errorf("err %v", err)
	}
}

func TestBadFiguresAreRejectedPerRow(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:46:40Z","cpu":101}` + "\n")
	b.WriteString(`{"v":1,"run":"r","seq":2,"t":"2001-09-09T01:46:55Z","cpu":-1}` + "\n")
	b.WriteString(`{"v":1,"run":"r","seq":3,"t":"2001-09-09T01:47:10","cpu":5}` + "\n") // no zone
	b.WriteString("not json\n")
	at := time.Date(2001, 9, 9, 1, 47, 25, 0, time.UTC)
	for i := 0; i < 100; i++ {
		b.WriteString(`{"v":1,"run":"r","seq":` + itoa(int64(5+i)) + `,"t":"` + at.Format(time.RFC3339) + `","cpu":5}` + "\n")
		at = at.Add(15 * time.Second)
	}
	evs, rep, err := parse(t, b.String(), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 4 || len(counters(evs, "% Processor Time")) != 100 {
		t.Errorf("rejected %d, cpu events %d", rep.Rejected, len(counters(evs, "% Processor Time")))
	}
}

func TestWindowKeepsOnlyOverlappingEvents(t *testing.T) {
	w := importer.Window{From: time.Date(2001, 9, 9, 1, 46, 50, 0, time.UTC), To: time.Date(2001, 9, 9, 1, 47, 0, 0, time.UTC)}
	evs, rep, err := parse(t, good, importer.Options{Window: w})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if !w.Overlaps(e.Start, e.End) {
			t.Errorf("event outside the window: %v", e.Start)
		}
	}
	if !contains(rep.KnownGaps, importer.GapOutsideWindow) {
		t.Errorf("known gaps %v", rep.KnownGaps)
	}
}

func TestDetect(t *testing.T) {
	i := New()
	if i.Detect([]byte(good), "bb-20010909-01.ndjson") < 90 {
		t.Error("a recording must be detected")
	}
	for name, h := range map[string]string{
		"clef":  `{"@t":"2001-09-09T01:46:40Z","@mt":"x"}` + "\n",
		"other": `{"v":1,"name":"x"}` + "\n",
		"csv":   "a,b,c\n1,2,3\n",
	} {
		if s := i.Detect([]byte(h), "x.txt"); s != 0 {
			t.Errorf("%s scored %d", name, s)
		}
	}
	if !bytes.Contains([]byte(good), []byte(`"seq"`)) {
		t.Fatal("fixture")
	}
}
