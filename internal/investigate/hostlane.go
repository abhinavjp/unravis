package investigate

import (
	"fmt"
	"sort"
	"time"
)

// The host lane: what a Blackbox recording says about the host in the two
// windows, and the gap rule (decision A35) applied to it. The engine stays pure:
// the server reads the recording and hands over HostInput, a plain list of
// samples. Nothing here reads a file, a share or a port.

// Host lane rules. They are local to this lane; the audit's own limits are in
// Thresholds.
const (
	// hostLateIntervals is the recorder's own rule for a gap: more than this many
	// intervals between two samples (blackbox.LateIntervals).
	hostLateIntervals = 3
	// hostDefaultInterval is used when the Blackbox did not say.
	hostDefaultInterval = 15
	// hostCPUHigh is the whole-machine CPU percent a minute must reach to count as
	// busy on its own; hostCPURise is how far above the reference median a minute
	// must be to count as busy against the reference.
	hostCPUHigh = 85.0
	hostCPURise = 25.0
	// hostCPUFlat is the percent every incident minute must stay under for the host
	// CPU to count as flat.
	hostCPUFlat = 60.0
	// hostOnsetRun is how many consecutive busy minutes make an onset.
	hostOnsetRun = 3
	// hostMinCoverage is the share of the incident window's minutes that must hold
	// a CPU reading before the lane reads at all.
	hostMinCoverage = 0.5
)

// HostProc is one process row of a sample: its name and its CPU share.
type HostProc struct {
	Name   string
	CPUPct *float64
}

// HostSample is one Blackbox sample, reduced to what the lane reads. A value the
// recorder could not read is nil and is never treated as zero.
type HostSample struct {
	T     time.Time
	CPU   *float64
	Procs []HostProc
}

// HostInput is a Blackbox recording as the engine sees it.
type HostInput struct {
	// Name is the Blackbox name, shown in the sources list.
	Name            string
	IntervalSeconds int
	// First and Last are the recording's first and last sample (zero when unknown).
	First, Last time.Time
	Samples     []HostSample
}

// HostResult is what the host lane concludes.
type HostResult struct {
	// Lanes holds H1 only: the Blackbox has no counters for the other host suspects.
	Lanes map[string]Tri
	// Onset is when host CPU first stayed busy in the incident window (nil if never,
	// or not readable).
	Onset *time.Time
	// Gaps are the stretches without a sample inside the windows (A35).
	Gaps []Span
	// TopProcess names the busiest process over the busy minutes, when processes
	// were recorded.
	TopProcess string
	// Covered is the number of incident minutes that hold a CPU reading.
	Covered int
	Note    string
}

// HostGaps finds the gaps of a recording inside one window. A gap is a stretch of
// more than three intervals without a sample, including the stretch between the
// window's edge and the nearest sample (a recording that began late, or ended
// early, leaves the rest of the window without data). Missing minutes are never
// counted as zero. A gap of GapWarnMinutes or fewer is "short" (warns); a longer
// one is "long" (those minutes cannot be tested).
func HostGaps(h HostInput, w Window, label string, warnMinutes int) []Span {
	if !w.Valid() {
		return nil
	}
	interval := h.IntervalSeconds
	if interval <= 0 {
		interval = hostDefaultInterval
	}
	limit := time.Duration(hostLateIntervals*interval) * time.Second
	times := hostTimes(h.Samples)

	var out []Span
	add := func(from, to time.Time) {
		if from.Before(w.Start) {
			from = w.Start
		}
		if to.After(w.End) {
			to = w.End
		}
		d := to.Sub(from)
		if d <= limit {
			return
		}
		kind := "long"
		if d <= time.Duration(warnMinutes)*time.Minute {
			kind = "short"
		}
		out = append(out, Span{
			Window: label, Start: from.UTC(), End: to.UTC(),
			Minutes: int((d + time.Minute - 1) / time.Minute), Kind: kind,
		})
	}

	// Nothing recorded at all: the whole window is one gap.
	if len(times) == 0 {
		add(w.Start, w.End)
		return out
	}
	// Edges. The sample before the window starts the stretch that opens it.
	i := sort.Search(len(times), func(i int) bool { return !times[i].Before(w.Start) })
	switch {
	case i == len(times):
		// Every sample is before the window.
		add(times[len(times)-1], w.End)
		return out
	case i == 0:
		add(w.Start, times[0])
	default:
		add(times[i-1], times[i])
	}
	// Between samples inside the window.
	for k := i; k+1 < len(times) && times[k].Before(w.End); k++ {
		add(times[k], times[k+1])
	}
	// The stretch that closes the window.
	j := sort.Search(len(times), func(j int) bool { return !times[j].Before(w.End) })
	if j == len(times) {
		add(times[len(times)-1], w.End)
	}
	return out
}

func hostTimes(ss []HostSample) []time.Time {
	ts := make([]time.Time, 0, len(ss))
	for _, s := range ss {
		if !s.T.IsZero() {
			ts = append(ts, s.T.UTC())
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	out := ts[:0]
	for i, t := range ts {
		if i == 0 || !t.Equal(ts[i-1]) {
			out = append(out, t)
		}
	}
	return out
}

type minuteStat struct {
	sum   float64
	n     int
	procs map[string]float64
}

// hostMinutes averages the CPU readings of each minute. A minute with no reading
// is absent from the map, never zero.
func hostMinutes(ss []HostSample, w Window) map[time.Time]*minuteStat {
	out := map[time.Time]*minuteStat{}
	for _, s := range ss {
		if s.CPU == nil || !w.Contains(s.T.UTC()) {
			continue
		}
		m := minuteOf(s.T)
		st := out[m]
		if st == nil {
			st = &minuteStat{procs: map[string]float64{}}
			out[m] = st
		}
		st.sum += *s.CPU
		st.n++
		for _, p := range s.Procs {
			if p.CPUPct != nil && p.Name != "" {
				st.procs[p.Name] += *p.CPUPct
			}
		}
	}
	return out
}

func (m *minuteStat) mean() float64 { return m.sum / float64(m.n) }

func medianOf(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2], true
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2, true
}

// HostLane reads a recording against the two windows of in. H1 (host CPU
// contention) is Yes when host CPU stayed busy for a run of minutes in the
// incident window, No when every incident minute was well under the busy mark
// and no long gap hides a minute, and Unknown otherwise. A window the recording
// does not cover well enough reads Unknown: unknown is never "did not move".
func HostLane(in Input, h HostInput) HostResult {
	res := HostResult{Lanes: map[string]Tri{}}
	warn := Current().GapWarnMinutes
	res.Gaps = append(res.Gaps, HostGaps(h, in.Reference, "reference", warn)...)
	res.Gaps = append(res.Gaps, HostGaps(h, in.Incident, "incident", warn)...)
	sort.SliceStable(res.Gaps, func(i, j int) bool { return res.Gaps[i].Start.Before(res.Gaps[j].Start) })

	if !in.Incident.Valid() {
		res.Lanes["H1"] = Unknown
		return res
	}
	inc := hostMinutes(h.Samples, in.Incident)
	res.Covered = len(inc)
	total := in.Incident.minutes()
	if total == 0 || float64(len(inc))/float64(total) < hostMinCoverage {
		res.Lanes["H1"] = Unknown
		res.Note = "The Blackbox has CPU readings for too few minutes of the incident window to read the host."
		return res
	}

	base, haveBase := 0.0, false
	if in.Reference.Valid() {
		ref := hostMinutes(h.Samples, in.Reference)
		var means []float64
		for _, st := range ref {
			means = append(means, st.mean())
		}
		if len(means) >= hostOnsetRun {
			base, haveBase = medianOf(means)
		}
	}
	busy := func(v float64) bool { return v >= hostCPUHigh || (haveBase && v >= base+hostCPURise) }

	// Walk the incident minutes in order. A missing minute breaks a run: it is not
	// a quiet minute, so it neither extends nor ends the count as zero.
	minutes := make([]time.Time, 0, len(inc))
	for m := range inc {
		minutes = append(minutes, m)
	}
	sort.Slice(minutes, func(i, j int) bool { return minutes[i].Before(minutes[j]) })
	var onset *time.Time
	run := 0
	maxMean := 0.0
	for k, m := range minutes {
		v := inc[m].mean()
		if v > maxMean {
			maxMean = v
		}
		if k > 0 && m.Sub(minutes[k-1]) > time.Minute {
			run = 0
		}
		if busy(v) {
			run++
			if run == hostOnsetRun && onset == nil {
				t := minutes[k-hostOnsetRun+1]
				onset = &t
			}
		} else {
			run = 0
		}
	}
	longInIncident := false
	for _, g := range res.Gaps {
		if g.Window == "incident" && g.Kind == "long" {
			longInIncident = true
		}
	}
	switch {
	case onset != nil:
		res.Lanes["H1"] = Yes
		res.Onset = onset
		res.TopProcess = topProcess(inc, minutes, busy)
		res.Note = "Host CPU stayed busy from " + fmtT(*onset) + "."
		if res.TopProcess != "" {
			res.Note += " The busiest process was " + res.TopProcess + "."
		}
	case maxMean < hostCPUFlat && !longInIncident:
		res.Lanes["H1"] = No
		res.Note = fmt.Sprintf("Host CPU stayed under %.0f%% in every minute that was recorded.", hostCPUFlat)
	default:
		res.Lanes["H1"] = Unknown
		if longInIncident {
			res.Note = "A long gap in the recording hides minutes of the incident window, so the host cannot be read as flat."
		} else {
			res.Note = "Host CPU was neither clearly busy nor clearly flat."
		}
	}
	return res
}

func topProcess(inc map[time.Time]*minuteStat, minutes []time.Time, busy func(float64) bool) string {
	total := map[string]float64{}
	for _, m := range minutes {
		if !busy(inc[m].mean()) {
			continue
		}
		for name, v := range inc[m].procs {
			total[name] += v
		}
	}
	best, bestV := "", 0.0
	for name, v := range total {
		if v > bestV || (v == bestV && best != "" && name < best) {
			best, bestV = name, v
		}
	}
	return best
}

// ApplyHost reads the recording and puts the answer on in: H1 in Lanes, the host
// onset in LaneOnsets, the Blackbox in LaneSources and the gaps in LaneGaps.
func (in *Input) ApplyHost(h HostInput) HostResult {
	res := HostLane(*in, h)
	if in.Lanes == nil {
		in.Lanes = map[string]Tri{}
	}
	for id, v := range res.Lanes {
		in.Lanes[id] = v
	}
	if res.Onset != nil {
		if in.LaneOnsets == nil {
			in.LaneOnsets = map[string]time.Time{}
		}
		in.LaneOnsets["host"] = *res.Onset
	}
	name := h.Name
	if name == "" {
		name = "Blackbox"
	} else {
		name = "Blackbox " + name
	}
	in.LaneSources = append(in.LaneSources, name)
	if len(res.Gaps) > 0 {
		if in.LaneGaps == nil {
			in.LaneGaps = map[string][]Span{}
		}
		in.LaneGaps["host"] = res.Gaps
	}
	return res
}

// laneGapStep applies the gap rule (A35) to a lane step. Any gap makes the step
// at most Partly ready. The minutes of a long gap cannot be tested and are listed
// on the step; they are never counted as zero.
func laneGapStep(st *Step, src string, gaps []Span) {
	if len(gaps) == 0 {
		return
	}
	short, long := 0, 0
	var longSpans []Span
	for _, g := range gaps {
		if g.Kind == "long" {
			long++
			longSpans = append(longSpans, g)
		} else {
			short++
		}
	}
	var note string
	switch {
	case long > 0 && short > 0:
		note = fmt.Sprintf("The %s data has %d long gap(s), which cannot be tested, and %d short gap(s), which are left out of the readings.", src, long, short)
	case long > 0:
		note = fmt.Sprintf("The %s data has %d long gap(s); those minutes cannot be tested and are never counted as zero.", src, long)
	default:
		note = fmt.Sprintf("The %s data has %d short gap(s); the minutes that exist are used and the missing ones are left out, never counted as zero.", src, short)
	}
	st.degrade(PartlyReady, note)
	st.UntestedSpans = append(st.UntestedSpans, longSpans...)
}
