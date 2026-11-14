package server

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// BBX-5-16 (BL-103): the Blackbox source of the database lane. When the case
// uses a Blackbox whose samples carry SQL host counters for the window, the SQL
// lane can be drawn from that sampling: activity blocks estimated from
// BatchRequestsPerSec, source key "blackbox", the fixed label and the
// first-sample timestamp. No query text, no instance names, no error text.
// Without such data the lane falls through to the existing held / qs-file /
// api-seen / not-recorded chain unchanged; it is never a zero lane. The samples
// are read on demand through the same sealed-list reader the host lane uses;
// nothing here writes or polls.
//
// Selection rule (W12-03, documented): an explicit sourceIds selection reads
// the Blackbox ONLY when its own source tag is among the selected sources; a
// selected import is never displaced by an unselected recorder. Query Store
// duration evidence (a held day, a layered lane or a qs-file import) outranks
// the machine-wide batch throughput and is decided BEFORE this lane runs. When
// the Blackbox does fill the lane, imported event markers (XE slow statements)
// are preserved as separately attributed markers — batch throughput, Query
// Store duration and XE markers are different measures and are never added
// together — and a PARTIAL Blackbox read never replaces an imported lane that
// already counted activity.
//
// Coverage contract (W12-01/W12-02): a bucket is complete only when known,
// whole SQL observations tile its whole clipped span. An internal SQL gap, a
// skipped optional group, a lost instance, a rejected number, a restart, a
// corrupt/torn/newer/capped/limited read all leave the affected buckets
// incomplete with a fixed warning. Unobserved time is never zero activity.
// Counts are rate x OBSERVED interval, apportioned by overlap into clipped
// buckets, and are labelled estimates — not durations, not concurrency.

const (
	tracingReasonBlackbox = "blackbox"
	// tracingBlackboxLabel is the fixed sentence; the cadence is the effective
	// sampling interval and the date is the recording's first sample.
	tracingBlackboxLabel = "Blackbox sampling every %d s since "
	// bbLaneGapWarning mirrors the host lane's incomplete-read warning (see
	// applyBlackboxLane), without the Blackbox name or any error text.
	bbLaneGapWarning = "Part of the Blackbox could not be read to the end. What was read is used, and the rest counts as a gap."
	// bbLaneCoverageWarning is the fixed warning for time the SQL counters did
	// not observe: an internal gap, a lost instance, a rejected reading, a
	// restart or a span the read cannot prove.
	bbLaneCoverageWarning = "Part of this window was not observed by the Blackbox SQL counters. Unobserved time is a gap, not zero activity."
	// bbLaneEstimateNote labels what the bars are: integrated throughput
	// estimates, never query durations or concurrency.
	bbLaneEstimateNote = "Bars estimate batch requests from the recorded per-second rate over the observed interval. They are not query durations or concurrency."
	// bbLaneMarkersNote says the imported markers stay separate from the bars.
	bbLaneMarkersNote = "The markers are the imported statements, kept separate; they are never added to the bars."
)

// The counters come from an untrusted recording, so every input of the block
// count is bounded before it can reach an int64. A real SQL Server never
// approaches these; a reading beyond them is corrupt, not activity, and is
// skipped rather than turned into a negative or wrapped count.
const (
	// bbSQLMaxRate bounds one instance's batch requests/sec.
	bbSQLMaxRate = 1e12
	// bbSQLMaxInterval bounds the manifest's sampling interval in seconds; an
	// interval outside 1..3600 falls back to the fixed 15 s cadence.
	bbSQLMaxInterval = 3600
	// bbSQLMaxCount bounds a sample's request count and the lane's running sums,
	// so rate x interval can never overflow int64 (a float beyond it is skipped,
	// never converted).
	bbSQLMaxCount = 1e15
)

// bbSQLRate sums the batch requests/sec of the instances that recorded it. ok is
// false when no instance carries the reading: a missing reading is never a 0.
// An absurd or non-finite reading is skipped the same way, never summed.
func bbSQLRate(s blackbox.Sample) (float64, bool) {
	rate, ok, _ := bbSQLReading(s)
	return rate, ok
}

// bbSQLReading is bbSQLRate plus whole: whole is true only when EVERY instance
// the sample lists carries a valid reading. A sample with one valid and one
// missing/rejected reading is a lower bound, not the machine total, so the lane
// marks its span partial instead of calling it complete (W12-01).
func bbSQLReading(s blackbox.Sample) (rate float64, ok, whole bool) {
	if s.SQLHost == nil {
		return 0, false, false
	}
	any, allValid := false, true
	for _, inst := range s.SQLHost.Instances {
		v := inst.BatchRequestsPerSec
		if v == nil || *v < 0 || *v > bbSQLMaxRate || math.IsNaN(*v) || math.IsInf(*v, 0) {
			allValid = false
			continue
		}
		rate += *v
		any = true
	}
	return rate, any, any && allValid
}

// bbLaneEntry finds the saved Blackbox the case names, honouring the request's
// source selection (W12-03): an explicit selection qualifies only a Blackbox
// whose own cs_ tag was selected (the first such in request order); a default
// call uses the first blackbox source tag, matched by name in the saved list
// (the host lane's rule). A case without one, or a name no longer listed, has
// no Blackbox lane source — and an unselected Blackbox is never even opened.
func (h *handler) bbLaneEntry(ctx context.Context, c casestore.Case, sel tracingSelection) (bbsaved.Entry, bool) {
	if h.bb == nil {
		return bbsaved.Entry{}, false
	}
	t, ok := c.(casestore.Tagger)
	if !ok {
		return bbsaved.Entry{}, false
	}
	tags, err := t.Sources(ctx)
	if err != nil {
		return bbsaved.Entry{}, false
	}
	name := ""
	if sel.Explicit {
		selected := map[string]bool{}
		for _, ref := range sel.Refs {
			selected[ref.SourceID] = true
		}
		for _, tag := range tags {
			if tag.Kind == blackboxTagKind && selected[tracingTagID(tag.ID)] {
				if n := strings.TrimSpace(tag.Label); n != "" {
					name = n
					break
				}
			}
		}
	} else {
		for _, tag := range tags {
			if tag.Kind == blackboxTagKind {
				if n := strings.TrimSpace(tag.Label); n != "" {
					name = n
					break
				}
			}
		}
	}
	if name == "" {
		return bbsaved.Entry{}, false
	}
	entries, err := h.bb.deps.Store.List()
	if err != nil {
		return bbsaved.Entry{}, false
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return bbsaved.Entry{}, false
}

// bbSQLObs is one accepted SQL observation. The rate was measured over the span
// (start, t] — the previous tick of the SAME recorder run to this sample — and
// known says that span is proven (a plausible measured interval, not a restart
// or a gap). whole says every instance seen so far reported a valid reading
// here, so the sum is the machine total and not a lower bound.
type bbSQLObs struct {
	t     time.Time
	start time.Time
	known bool
	whole bool
	rate  float64
}

// bbLaneSamples reads the window's samples of one saved Blackbox (plus the
// boundary observations before the window start and at the window end, so the
// first and last measured spans are known) and turns them into SQL
// observations. first and last are the SQL coverage — the first and last
// accepted SQL observation, NOT the host manifest's bounds: a host that kept
// sampling while SQL went silent does not cover the silence (W12-01). rep says
// what the read could not get. ok is false when nothing could be read or no
// sample carries the reading: the lane then falls through, quietly.
func (h *handler) bbLaneSamples(ctx context.Context, e bbsaved.Entry, axis tracingAxis) (obs []bbSQLObs, cadence int, first, last, since time.Time, rep blackbox.ReadReport, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, bbReadTimeout)
	defer cancel()
	src, err := h.bb.source(e)
	if err != nil {
		return nil, 0, time.Time{}, time.Time{}, time.Time{}, rep, false
	}
	rd, err := blackbox.ProbeSource(ctx, src, h.bb.now)
	if err != nil {
		return nil, 0, time.Time{}, time.Time{}, time.Time{}, rep, false
	}
	// The effective cadence, not the configured base: the light budget widens
	// and narrows the tick (W12-02). It bounds the lookback and the plausible
	// span; the spans themselves come from the observed sample times.
	cadence = rd.Manifest.CadenceSeconds()
	if cadence < 1 || cadence > bbSQLMaxInterval {
		cadence = blackbox.DefaultIntervalSecond
	}
	lookback := time.Duration(2*cadence) * time.Second
	if lookback > time.Duration(bbSQLMaxInterval)*time.Second {
		lookback = time.Duration(bbSQLMaxInterval) * time.Second
	}
	// The observation AT the window end measures the last span of the window,
	// so the read includes it (the window itself stays half open).
	samples, rep, err := blackbox.ReadSamples(ctx, src, rd.Manifest, axis.Start.Add(-lookback), axis.End.Add(time.Nanosecond))
	if err != nil {
		return nil, 0, time.Time{}, time.Time{}, time.Time{}, rep, false
	}
	if rd.Manifest.FirstSample != nil && !rd.Manifest.FirstSample.IsZero() {
		since = rd.Manifest.FirstSample.UTC()
	}
	gapLimit := time.Duration(blackbox.LateIntervals*cadence) * time.Second
	var prevT time.Time
	var prevRun string
	havePrev := false
	seen := map[string]bool{} // instances that ever contributed a valid reading
	for _, s := range samples {
		rate, has, whole := bbSQLReading(s)
		if has {
			// Per-instance coverage: an instance that contributed before but is
			// silent now makes this sum a lower bound (partial instance loss).
			names := map[string]bool{}
			for _, inst := range s.SQLHost.Instances {
				v := inst.BatchRequestsPerSec
				if v != nil && *v >= 0 && *v <= bbSQLMaxRate && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
					names[inst.Instance] = true
					seen[inst.Instance] = true
				}
			}
			covers := true
			for n := range seen {
				if !names[n] {
					covers = false
				}
			}
			known := havePrev && s.Run == prevRun && s.T.After(prevT) && s.T.Sub(prevT) <= gapLimit
			o := bbSQLObs{t: s.T.UTC(), rate: rate, whole: whole && covers, known: known}
			if known {
				o.start = prevT.UTC()
			}
			obs = append(obs, o)
		}
		// EVERY sample advances the tick clock, even one without SQL: the rate
		// at the next SQL sample was measured from the previous tick, not from
		// the previous SQL sample.
		prevT, prevRun, havePrev = s.T, s.Run, true
	}
	if len(obs) == 0 {
		return nil, 0, time.Time{}, time.Time{}, time.Time{}, rep, false
	}
	if since.IsZero() {
		since = obs[0].t
	}
	return obs, cadence, obs[0].t, obs[len(obs)-1].t, since, rep, true
}

// blackboxSQLLane fills the SQL lane from the case's Blackbox sampling and
// reports true, or leaves the lane untouched and reports false so the existing
// source chain runs unchanged. Each observation's rate is integrated over its
// OBSERVED span and apportioned by overlap into the clipped buckets it touches
// (W12-02); a span the read cannot prove contributes no count and leaves its
// buckets incomplete. A read is bounded by bbReadTimeout and the reader's own
// file/byte/sample caps; nothing is kept between requests. Time the recording
// does not cover is a gap, never a zero.
func (h *handler) blackboxSQLLane(ctx context.Context, c casestore.Case, axis tracingAxis, sel tracingSelection, lane tracingLane) (tracingLane, bool) {
	if axis.WidthMs <= 0 {
		return lane, false
	}
	// Query Store duration evidence outranks machine-wide batch throughput:
	// those lanes are decided before this one and are never displaced.
	switch lane.ReasonCode {
	case tracingReasonHeldSQL, tracingReasonLayeredSQL, tracingReasonQSFile:
		return lane, false
	}
	e, ok := h.bbLaneEntry(ctx, c, sel)
	if !ok {
		return lane, false
	}
	// What the imported lane already counted, before anything is replaced.
	importedScanned := lane.Scanned
	obs, cadence, first, last, since, rep, ok := h.bbLaneSamples(ctx, e, axis)
	if !ok {
		return lane, false
	}
	perCell := map[int]float64{}
	covered := map[int]float64{} // ms of each bucket tiled by known, whole spans
	dirty := map[int]bool{}      // buckets an unproven or partial span touches
	rejected := false
	var totalF float64
	winStart, winEnd := axis.Start.UnixMilli(), axis.End.UnixMilli()
	axisStart := axis.AxisStart.UnixMilli()
	cellOf := func(ms int64) int {
		k := int((ms - axisStart) / axis.WidthMs)
		if k < 0 || k >= axis.Buckets {
			return -1
		}
		return k
	}
	// markSpan flags every bucket the stretch (fromMs, toMs] touches.
	markSpan := func(fromMs, toMs int64) {
		if toMs <= fromMs {
			return
		}
		k0, k1 := cellOf(fromMs), cellOf(toMs-1)
		if k0 < 0 {
			k0 = 0
		}
		if k1 >= axis.Buckets {
			k1 = axis.Buckets - 1
		}
		for k := k0; k <= k1; k++ {
			dirty[k] = true
		}
	}
	prevObs := winStart
	for _, o := range obs {
		tMs := o.t.UnixMilli()
		if !o.known {
			// An unproven span (restart, gap, first observation): no count, and
			// the stretch since the last observation is unobserved, not zero.
			markSpan(prevObs, tMs)
			prevObs = tMs
			continue
		}
		sMs := o.start.UnixMilli()
		// SQL silence between the previous observation and this span's start is
		// an internal gap even when the host kept sampling.
		markSpan(prevObs, sMs)
		prevObs = tMs
		spanMs := tMs - sMs
		n := o.rate * float64(spanMs) / 1000
		if n < 0 || n > bbSQLMaxCount {
			// A rejected number is never an observed zero: it downgrades the
			// coverage of the span it stood for (W12-01).
			rejected = true
			markSpan(sMs, tMs)
			continue
		}
		lo, hi := sMs, tMs
		if lo < winStart {
			lo = winStart
		}
		if hi > winEnd {
			hi = winEnd
		}
		if hi <= lo {
			continue
		}
		k0, k1 := cellOf(lo), cellOf(hi-1)
		if k0 < 0 {
			k0 = 0
		}
		if k1 >= axis.Buckets {
			k1 = axis.Buckets - 1
		}
		for k := k0; k <= k1; k++ {
			from, to, _, ok := axis.Bucket(k)
			if !ok {
				continue
			}
			a, b := from.UnixMilli(), to.UnixMilli()
			if lo > a {
				a = lo
			}
			if hi < b {
				b = hi
			}
			if b <= a {
				continue
			}
			overlap := float64(b - a)
			add := o.rate * overlap / 1000
			if perCell[k] > bbSQLMaxCount-add || totalF > bbSQLMaxCount-add {
				rejected = true
				dirty[k] = true
				continue
			}
			perCell[k] += add
			totalF += add
			if o.whole {
				covered[k] += overlap
			} else {
				dirty[k] = true
			}
		}
	}
	// Time after the last observation is unobserved too.
	markSpan(prevObs, winEnd)

	perCellInt := make(map[int]int64, len(perCell))
	var total int64
	for k, v := range perCell {
		n := int64(math.Round(v))
		perCellInt[k] = n
		total += n
	}
	since = since.UTC()
	buckets := tracingBuckets(axis, perCellInt, nil)
	coverageGap := false
	for i := range buckets {
		k := cellOf(buckets[i].Start.UnixMilli())
		from, to, _, ok := axis.Bucket(k)
		complete := ok && !dirty[k]
		if ok {
			need := float64(to.Sub(from).Milliseconds())
			if covered[k] < need {
				complete = false
			}
		}
		// The SQL coverage envelope, not the host manifest's bounds.
		if buckets[i].Start.Before(first) || buckets[i].End.After(last) {
			complete = false
		}
		buckets[i].Complete = complete
		if !complete {
			coverageGap = true
		}
	}
	complete := !coverageGap
	truncated := false
	// A read that lost files, hit a cap or a budget, or saw corruption/newer
	// lines cannot speak for ANY bucket: what was read is kept, the rest is a
	// gap (the host lane's rule, widened by W12-01).
	if rep.CoverageIncomplete() {
		complete, truncated = false, true
		for i := range buckets {
			buckets[i].Complete = false
		}
	}
	// A partial machine-wide estimate never erases an imported lane that already
	// counted activity for the window (W12-03): the import stays, untouched.
	if !complete && importedScanned > 0 {
		return lane, false
	}
	// Imported markers are different measures: they stay on the lane, separately
	// attributed, never summed into the batch estimates (W12-03).
	markers := len(lane.Events) > 0
	reason := bbLaneEstimateNote
	if rep.CoverageIncomplete() {
		reason += " " + bbLaneGapWarning
	}
	if coverageGap || rejected {
		reason += " " + bbLaneCoverageWarning
	}
	bbRef := tracingRef{SourceID: tracingReasonBlackbox, Label: fmt.Sprintf(tracingBlackboxLabel, cadence) + since.Format("2 Jan 2006")}
	if markers {
		lane.Sources = append(append(make([]tracingRef, 0, len(lane.Sources)+1), lane.Sources...), bbRef)
		reason += " " + bbLaneMarkersNote
	} else {
		lane.Sources = []tracingRef{bbRef}
		lane.Events = []tracingEvent{}
		lane.Hidden = 0
	}
	lane.Label = "SQL Server"
	lane.State = calltrace.StateEvents
	lane.Origin, lane.Consistency = tracingOriginCase, tracingConsStored
	lane.AsOf = last.UTC()
	lane.ReasonCode, lane.Reason = tracingReasonBlackbox, reason
	lane.Scanned = total
	lane.Estimates = true
	lane.Complete, lane.Truncated = complete, truncated
	lane.Buckets = buckets
	lane.BlockSeconds = 0
	lane.Since = &since
	return lane, true
}