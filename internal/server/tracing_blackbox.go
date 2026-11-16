package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"sync"
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
	// bbSQLMaxCadenceSeconds is the widest interval the recorder can actually
	// tick at: the light budget's ladder caps at 60 s (budgetLadder in
	// internal/blackbox/budget.go) and Config.Validate accepts a base of only
	// 5, 15, 30 or 60. It is NOT bbSQLMaxInterval, which is merely the sanity
	// bound on what a manifest may claim.
	bbSQLMaxCadenceSeconds = 60
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

// bbSQLGroupSkipped reports whether a sample's Missing names a tick where the
// sqlHost group did not collect. The entries matched are the exact strings the
// group collector writes (internal/blackbox/groups.go): "sqlHost" (budget
// skip or failed collect), "sqlHost: slow" (slow-group skip or timeout) and
// "sqlHost: busy" (the previous worker is still inside the provider). No other
// variant exists; they are matched verbatim, in one place.
func bbSQLGroupSkipped(missing []string) bool {
	for _, m := range missing {
		switch m {
		case "sqlHost", "sqlHost: slow", "sqlHost: busy":
			return true
		}
	}
	return false
}

// bbSQLObs is one accepted SQL observation. The rate was measured over the span
// (start, t] — the last PDH collect of the SAME recorder run to this sample,
// which is NOT the previous tick when the sqlHost group was skipped there —
// and known says that span is proven (a plausible measured interval, not a
// restart, a gap or a skipped collect). whole says every instance seen so far
// reported a valid reading here, so the sum is the machine total and not a
// lower bound.
type bbSQLObs struct {
	t     time.Time
	start time.Time
	known bool
	whole bool
	rate  float64
}

// bbLaneRead is one window of one Blackbox read whole: the extracted SQL
// observations with their spans and known/whole marks, plus everything else
// bbLaneSamples answers with. The extraction below is what fills it, unchanged,
// so a replayed read answers exactly like the read it came from: a span the read
// could not prove (CORR-1's gap limit, CORR-3's tick that skipped the sqlHost
// group) stays unknown in the cache too, and cannot become known by being
// served again. The read report travels with it for the same reason: a read that
// could not get to the end must not come back looking whole.
type bbLaneRead struct {
	obs                []bbSQLObs
	rep                blackbox.ReadReport
	cadence            int
	first, last, since time.Time
}

// bbLaneKey names one read: the saved Blackbox, the manifest the read was made
// against and the window it was asked for.
type bbLaneKey struct {
	entry      string
	sig        [sha256.Size]byte
	start, end int64
}

const (
	// bbLaneCacheTTL bounds how long a read stays reusable after its signature
	// stopped changing. A recording that moved on moves the file list or the last
	// sample, and the key catches that at once; what the key cannot see is a file
	// that went away while the manifest still listed it, and only time forgets it.
	bbLaneCacheTTL = time.Minute
	// bbLaneCacheEntries and bbLaneCacheObs bound what the kept reads hold
	// together, the way the rollup cache does (PF-B-03): eight windows, and at
	// most two reads of the reader's own sample ceiling of observations, oldest
	// window out first. One read can never be wider than that ceiling, so the
	// memory is fixed whatever window the timeline is shown over.
	bbLaneCacheEntries = 8
	bbLaneCacheObs     = 200000
)

type bbLaneEntry struct {
	key  bbLaneKey
	at   time.Time
	nobs int
	read *bbLaneRead
}

// bbLaneFlight is the read one caller is doing while the rest wait on done for
// its answer, so a burst on one window opens the files once. The stdlib has no
// singleflight and go.mod is not ours for this.
type bbLaneFlight struct {
	done chan struct{}
	read bbLaneRead
	ok   bool
}

type bbLaneCache struct {
	mu      sync.Mutex
	now     func() time.Time // for tests
	entries []bbLaneEntry    // newest first
	flights map[bbLaneKey]*bbLaneFlight
}

// bbLaneReads holds the window reads of the process. One server runs one
// handler, and a key names its saved Blackbox, so two recordings or two saved
// lists cannot answer for each other.
var bbLaneReads bbLaneCache

func (c *bbLaneCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// run answers key from the cached read, or does it once when several requests
// ask for the same window at the same time. A read that failed or could not
// speak for its whole window is never kept: the next request may find the share
// back, and a remembered gap would hold the lane partial for nothing.
func (c *bbLaneCache) run(ctx context.Context, key bbLaneKey, do func() (bbLaneRead, bool)) (bbLaneRead, bool) {
	c.mu.Lock()
	if read, ok := c.getLocked(key); ok {
		c.mu.Unlock()
		return read, true
	}
	if f := c.flights[key]; f != nil {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.read, f.ok
		case <-ctx.Done():
			return bbLaneRead{}, false
		}
	}
	f := &bbLaneFlight{done: make(chan struct{})}
	if c.flights == nil {
		c.flights = map[bbLaneKey]*bbLaneFlight{}
	}
	c.flights[key] = f
	c.mu.Unlock()

	read, ok := do()

	c.mu.Lock()
	delete(c.flights, key)
	if ok && !read.rep.CoverageIncomplete() {
		kept := read
		c.pushLocked(key, &kept)
	}
	c.mu.Unlock()
	// Written before done closes: every waiter happens-after the close.
	f.read, f.ok = read, ok
	close(f.done)
	return read, ok
}

// getLocked returns the read of key, ignoring the entries past their TTL
// (pushLocked is what drops them). The caller must hold the mutex.
func (c *bbLaneCache) getLocked(key bbLaneKey) (bbLaneRead, bool) {
	now := c.clock()
	for _, e := range c.entries {
		if now.Sub(e.at) >= bbLaneCacheTTL {
			continue
		}
		if e.key == key {
			return *e.read, true
		}
	}
	return bbLaneRead{}, false
}

// pushLocked keeps read as the newest entry, dropping the aged ones, any older
// copy of the same key and, past bbLaneCacheEntries windows or bbLaneCacheObs
// observations together, the oldest. A read too large for the whole budget is
// not kept, but the fresh ones around it are. The caller must hold the mutex.
func (c *bbLaneCache) pushLocked(key bbLaneKey, read *bbLaneRead) {
	now := c.clock()
	n := len(read.obs)
	fresh := make([]bbLaneEntry, 0, bbLaneCacheEntries)
	if n > bbLaneCacheObs {
		for _, e := range c.entries {
			if e.key != key && now.Sub(e.at) < bbLaneCacheTTL {
				fresh = append(fresh, e)
			}
		}
		c.entries = fresh
		return
	}
	fresh = append(fresh, bbLaneEntry{key: key, at: now, nobs: n, read: read})
	total := n
	for _, e := range c.entries { // newest first
		if len(fresh) >= bbLaneCacheEntries || total+e.nobs > bbLaneCacheObs {
			break // this one and every older one go: oldest out first
		}
		if e.key != key && now.Sub(e.at) < bbLaneCacheTTL {
			fresh = append(fresh, e)
			total += e.nobs
		}
	}
	c.entries = fresh
}

// bbLaneSignature fingerprints the manifest a read is made against: the file
// list with the size, sample count and span of every file, the first and last
// sample, the runs and the cadence the tolerance, the lookback and the lane label
// come from. Any of those moving means the window has to be read again.
func bbLaneSignature(m blackbox.Manifest) [sha256.Size]byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|%s|%d|%d|", m.Format, m.Name, m.Host, m.IntervalSeconds, m.EffectiveIntervalSeconds)
	if m.FirstSample != nil {
		fmt.Fprintf(&b, "first=%d;", m.FirstSample.UnixNano())
	}
	if m.LastSample != nil {
		fmt.Fprintf(&b, "last=%d;", m.LastSample.UnixNano())
	}
	for _, f := range m.Files {
		fmt.Fprintf(&b, "f=%s,%d,%d,%d,%d;", f.Name, f.Bytes, f.Samples, f.First.UnixNano(), f.Last.UnixNano())
	}
	for _, r := range m.Runs {
		fmt.Fprintf(&b, "r=%s,%d;", r.ID, r.Start.UnixNano())
	}
	return sha256.Sum256([]byte(b.String()))
}

// bbLaneSamples reads the window's samples of one saved Blackbox (plus the
// boundary observations before the window start and at the window end, so the
// first and last measured spans are known) and turns them into SQL
// observations. first and last are the SQL coverage — the first and last
// accepted SQL observation, NOT the host manifest's bounds: a host that kept
// sampling while SQL went silent does not cover the silence (W12-01). rep says
// what the read could not get. ok is false when nothing could be read or no
// sample carries the reading: the lane then falls through, quietly.
//
// PERF-2: the manifest is read on every request (it is what the key is made
// from), but the window's files are opened and decoded once per signature: the
// repeat of a window is answered from the extracted observations.
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
	key := bbLaneKey{
		entry: e.ID,
		sig:   bbLaneSignature(rd.Manifest),
		start: axis.Start.UnixNano(),
		end:   axis.End.UnixNano(),
	}
	read, ok := bbLaneReads.run(ctx, key, func() (bbLaneRead, bool) { return bbLaneExtract(ctx, src, rd.Manifest, axis) })
	if !ok {
		return nil, 0, time.Time{}, time.Time{}, time.Time{}, read.rep, false
	}
	return read.obs, read.cadence, read.first, read.last, read.since, read.rep, true
}

// bbLaneExtract does the read bbLaneSamples caches: the window's samples, the
// span rules and the observations. It is the only place the sample files of a
// timeline request are opened.
func bbLaneExtract(ctx context.Context, src blackbox.Source, man blackbox.Manifest, axis tracingAxis) (bbLaneRead, bool) {
	var out bbLaneRead
	// The effective cadence, not the configured base: the light budget widens
	// and narrows the tick (W12-02). It names the lane; the spans themselves
	// come from the observed sample times.
	cadence := man.CadenceSeconds()
	if cadence < 1 || cadence > bbSQLMaxInterval {
		cadence = blackbox.DefaultIntervalSecond
	}
	out.cadence = cadence
	// CORR-1: the span tolerance is floored at the widest cadence the recorder
	// can reach, never taken from the manifest's LATEST value alone. The budget
	// steps back as well as widens, so a window sampled every 60 s can be read
	// against a manifest that has since stepped back to 15 s; judging those
	// spans by 3x15 s would call every one of them a gap and drop a fully
	// observed lane to unknown. A manifest may only WIDEN the tolerance (still
	// bounded by bbSQLMaxInterval above), never narrow it: narrowing it is the
	// same defect in the other direction. For every real recording - base 5,
	// 15, 30 or 60, ladder cap 60 - the tolerance is exactly 60 s. The lookback
	// reaches the first prior sample within two of those ticks, so the window's
	// first measured span has a start.
	tolerance := max(cadence, bbSQLMaxCadenceSeconds)
	lookback := time.Duration(2*tolerance) * time.Second
	if lookback > time.Duration(bbSQLMaxInterval)*time.Second {
		lookback = time.Duration(bbSQLMaxInterval) * time.Second
	}
	// The observation AT the window end measures the last span of the window,
	// so the read includes it (the window itself stays half open).
	samples, rep, err := blackbox.ReadSamples(ctx, src, man, axis.Start.Add(-lookback), axis.End.Add(time.Nanosecond))
	out.rep = rep
	if err != nil {
		return out, false
	}
	if man.FirstSample != nil && !man.FirstSample.IsZero() {
		out.since = man.FirstSample.UTC()
	}
	gapLimit := time.Duration(blackbox.LateIntervals*tolerance) * time.Second
	var prevT time.Time
	var prevRun string
	havePrev := false
	prevSkipped := false      // the previous tick did not collect the sqlHost group
	seen := map[string]bool{} // instances that ever contributed a valid reading
	var obs []bbSQLObs
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
			// CORR-3: a previous tick that skipped the sqlHost group did not
			// collect the PDH counter, so this rate was measured from an earlier
			// collect than prevT: the span (prevT, t] is not what was measured
			// and stays unknown, however short it is (CORR-1's widened gapLimit
			// must not wave it through).
			known := havePrev && !prevSkipped && s.Run == prevRun && s.T.After(prevT) && s.T.Sub(prevT) <= gapLimit
			o := bbSQLObs{t: s.T.UTC(), rate: rate, whole: whole && covers, known: known}
			if known {
				o.start = prevT.UTC()
			}
			obs = append(obs, o)
		}
		// EVERY sample advances the tick clock, even one without SQL: the rate
		// at the next SQL sample was measured from the last PDH collect, not
		// from the previous SQL sample. When THIS tick skipped the sqlHost
		// group there was no collect, so the next SQL span cannot be proven
		// from prevT (CORR-3).
		prevT, prevRun, havePrev = s.T, s.Run, true
		prevSkipped = bbSQLGroupSkipped(s.Missing)
	}
	if len(obs) == 0 {
		return bbLaneRead{rep: rep}, false
	}
	out.obs = obs
	out.first, out.last = obs[0].t, obs[len(obs)-1].t
	if out.since.IsZero() {
		out.since = obs[0].t
	}
	return out, true
}

// blackboxSQLLane fills the SQL lane from the case's Blackbox sampling and
// reports true, or leaves the lane untouched and reports false so the existing
// source chain runs unchanged. Each observation's rate is integrated over its
// OBSERVED span and apportioned by overlap into the clipped buckets it touches
// (W12-02); a span the read cannot prove contributes no count and leaves its
// buckets incomplete. A read is bounded by bbReadTimeout and the reader's own
// file/byte/sample caps; the window that was read is kept and answered again for
// the same window of the same recording (bbLaneSamples, PERF-2). Time the
// recording does not cover is a gap, never a zero.
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
