package investigate

import (
	"strings"
	"testing"
	"time"
)

var hostT0 = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

// hostSamples makes samples every 15 s from start to end (exclusive). cpu gives
// the percent at a time; skip says which times have no sample at all.
func hostSamples(start, end time.Time, cpu func(time.Time) *float64, skip func(time.Time) bool) []HostSample {
	var out []HostSample
	for t := start; t.Before(end); t = t.Add(15 * time.Second) {
		if skip != nil && skip(t) {
			continue
		}
		out = append(out, HostSample{T: t, CPU: cpu(t), Procs: []HostProc{{Name: "w3wp", CPUPct: cpu(t)}, {Name: "sqlservr", CPUPct: f64(3)}}})
	}
	return out
}

func f64(v float64) *float64 { return &v }

func hostInput() Input {
	return Input{
		Reference: Window{Start: hostT0, End: hostT0.Add(30 * time.Minute)},
		Incident:  Window{Start: hostT0.Add(30 * time.Minute), End: hostT0.Add(60 * time.Minute)},
	}
}

func calm(time.Time) *float64 { return f64(20) }

func spikeFrom(at time.Time) func(time.Time) *float64 {
	return func(t time.Time) *float64 {
		if !t.Before(at) {
			return f64(95)
		}
		return f64(20)
	}
}

func hostH(samples []HostSample) HostInput {
	return HostInput{Name: "web-b", IntervalSeconds: 15, Samples: samples}
}

func TestHostLaneBusyIncidentIsYesWithOnset(t *testing.T) {
	in := hostInput()
	at := hostT0.Add(40 * time.Minute)
	res := HostLane(in, hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), spikeFrom(at), nil)))
	if res.Lanes["H1"] != Yes {
		t.Fatalf("H1 = %v, want yes (%s)", res.Lanes["H1"], res.Note)
	}
	if res.Onset == nil || !res.Onset.Equal(at) {
		t.Fatalf("onset = %v, want %v", res.Onset, at)
	}
	if res.TopProcess != "w3wp" {
		t.Fatalf("top process = %q", res.TopProcess)
	}
	if len(res.Gaps) != 0 {
		t.Fatalf("unexpected gaps: %+v", res.Gaps)
	}
}

func TestHostLaneFlatCPUIsNo(t *testing.T) {
	res := HostLane(hostInput(), hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), calm, nil)))
	if res.Lanes["H1"] != No || res.Onset != nil {
		t.Fatalf("H1 = %v onset %v (%s)", res.Lanes["H1"], res.Onset, res.Note)
	}
}

func TestHostLaneNoSamplesIsUnknownAndOneLongGapPerWindow(t *testing.T) {
	res := HostLane(hostInput(), hostH(nil))
	if res.Lanes["H1"] != Unknown {
		t.Fatalf("H1 = %v", res.Lanes["H1"])
	}
	if len(res.Gaps) != 2 || res.Gaps[0].Kind != "long" || res.Gaps[0].Minutes != 30 {
		t.Fatalf("gaps = %+v", res.Gaps)
	}
}

// A gap of 2 minutes warns; the source stays usable (A35, short case).
func TestGapOfTwoMinutesIsShort(t *testing.T) {
	from := hostT0.Add(45 * time.Minute)
	skip := func(t time.Time) bool { return t.After(from) && t.Before(from.Add(2*time.Minute)) }
	in := hostInput()
	res := HostLane(in, hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), spikeFrom(hostT0.Add(40*time.Minute)), skip)))
	if len(res.Gaps) != 1 {
		t.Fatalf("gaps = %+v", res.Gaps)
	}
	g := res.Gaps[0]
	if g.Kind != "short" || g.Window != "incident" || g.Minutes != 2 {
		t.Fatalf("gap = %+v", g)
	}
	if res.Lanes["H1"] != Yes {
		t.Fatalf("a short gap must not stop the lane reading: %v", res.Lanes["H1"])
	}
}

// A gap of 3 minutes is long: those minutes cannot be tested (A35, long case).
func TestGapOfThreeMinutesIsLong(t *testing.T) {
	last := hostT0.Add(45 * time.Minute)
	skip := func(t time.Time) bool { return t.After(last) && t.Before(last.Add(3*time.Minute)) }
	res := HostLane(hostInput(), hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), calm, skip)))
	if len(res.Gaps) != 1 || res.Gaps[0].Kind != "long" || res.Gaps[0].Minutes != 3 {
		t.Fatalf("gaps = %+v", res.Gaps)
	}
	// Flat CPU with a hole in the incident window is not "flat": unknown is never no.
	if res.Lanes["H1"] != Unknown {
		t.Fatalf("H1 = %v, want unknown", res.Lanes["H1"])
	}
}

func TestGapMissingMinutesNeverCountAsQuiet(t *testing.T) {
	// The busy run is broken by a missing minute: two busy minutes, a hole, two more
	// busy minutes never make a run of three.
	busyFrom := hostT0.Add(40 * time.Minute)
	hole := func(t time.Time) bool {
		return !t.Before(busyFrom.Add(2*time.Minute)) && t.Before(busyFrom.Add(3*time.Minute))
	}
	end := busyFrom.Add(4 * time.Minute)
	cpu := func(t time.Time) *float64 {
		if t.Before(busyFrom) || !t.Before(end) {
			return f64(20)
		}
		return f64(95)
	}
	res := HostLane(hostInput(), hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), cpu, hole)))
	if res.Lanes["H1"] == Yes {
		t.Fatalf("a hole must break the run, not bridge it: %v", res.Lanes["H1"])
	}
}

func TestRecordingThatBeginsLateLeavesALeadingGap(t *testing.T) {
	in := hostInput()
	start := hostT0.Add(40 * time.Minute) // Blackbox began 10 minutes into the incident window
	res := HostLane(in, hostH(hostSamples(start, hostT0.Add(60*time.Minute), calm, nil)))
	var inc *Span
	for i := range res.Gaps {
		if res.Gaps[i].Window == "incident" {
			inc = &res.Gaps[i]
		}
	}
	if inc == nil || inc.Kind != "long" || inc.Minutes != 10 {
		t.Fatalf("gaps = %+v", res.Gaps)
	}
}

func TestHostGapsEmptyWindow(t *testing.T) {
	if g := HostGaps(HostInput{}, Window{}, "incident", 2); g != nil {
		t.Fatalf("gaps = %+v", g)
	}
}

func TestApplyHostFillsTheSeam(t *testing.T) {
	in := hostInput()
	last := hostT0.Add(45 * time.Minute)
	skip := func(t time.Time) bool { return t.After(last) && t.Before(last.Add(3*time.Minute)) }
	res := in.ApplyHost(hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), spikeFrom(hostT0.Add(35*time.Minute)), skip)))
	if in.Lanes["H1"] != Yes || in.LaneOnsets["host"].IsZero() {
		t.Fatalf("lanes %v onsets %v (%s)", in.Lanes, in.LaneOnsets, res.Note)
	}
	if len(in.LaneSources) != 1 || in.LaneSources[0] != "Blackbox web-b" {
		t.Fatalf("sources = %v", in.LaneSources)
	}
	if len(in.LaneGaps["host"]) != 1 {
		t.Fatalf("gaps = %+v", in.LaneGaps)
	}
}

func hostStep(t *testing.T, in Input) Step {
	t.Helper()
	res := Analyze(in)
	return res.Steps[9]
}

func TestStep9RendersShortGapAsPartlyReadyWithoutUntestedSpans(t *testing.T) {
	in := hostInput()
	from := hostT0.Add(45 * time.Minute)
	skip := func(t time.Time) bool { return t.After(from) && t.Before(from.Add(2*time.Minute)) }
	in.ApplyHost(hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), spikeFrom(hostT0.Add(40*time.Minute)), skip)))
	st := hostStep(t, in)
	if st.Readiness != PartlyReady {
		t.Fatalf("readiness = %v", st.Readiness)
	}
	if len(st.UntestedSpans) != 0 {
		t.Fatalf("a short gap lists no untested span: %+v", st.UntestedSpans)
	}
	if !strings.Contains(strings.Join(st.Reasons, " "), "short gap") {
		t.Fatalf("reasons = %v", st.Reasons)
	}
}

func TestStep9RendersLongGapWithUntestedSpans(t *testing.T) {
	in := hostInput()
	from := hostT0.Add(45 * time.Minute)
	skip := func(t time.Time) bool { return t.After(from) && t.Before(from.Add(3*time.Minute)) }
	in.ApplyHost(hostH(hostSamples(hostT0, hostT0.Add(60*time.Minute), spikeFrom(hostT0.Add(40*time.Minute)), skip)))
	st := hostStep(t, in)
	if st.Readiness != PartlyReady || len(st.UntestedSpans) != 1 || st.UntestedSpans[0].Minutes != 3 {
		t.Fatalf("step = %+v", st)
	}
	if !strings.Contains(strings.Join(st.Reasons, " "), "never counted as zero") {
		t.Fatalf("reasons = %v", st.Reasons)
	}
}

func TestStep9WithoutBlackboxIsUnchanged(t *testing.T) {
	st := hostStep(t, hostInput())
	if st.Readiness != CannotTestYet || len(st.UntestedSpans) != 0 {
		t.Fatalf("step = %+v", st)
	}
}
