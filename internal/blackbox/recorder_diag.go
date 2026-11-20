package blackbox

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"time"
)

// diagErrCode is the fixed word for why a round gave no sample. Never err.Error().
func diagErrCode(err error) string {
	switch {
	case errors.Is(err, errHostSlow):
		return "host-slow"
	case errors.Is(err, errHostBusy):
		return "host-busy"
	case errors.Is(err, errGroupPanic):
		return "panic"
	case errors.Is(err, os.ErrPermission):
		return "permission-denied"
	case isDiskFull(err):
		return "disk-full"
	default:
		return codeCollectFailed
	}
}

// diagRound takes one finished round into the log: a line of its own when it
// was notable, the totals for the next heartbeat always. Cheap by design: one
// small struct add per round, a line only when something was off.
func (r *Recorder) diagRound(now time.Time, rd roundDiag, start time.Time, paused bool) {
	d := r.o.Diag
	if d == nil {
		return
	}
	rd.Total = time.Since(start)
	interval := time.Duration(effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds)) * time.Second
	if r.agg.since.IsZero() {
		r.agg.since = now
		r.lastBeat = now
	}
	if !paused {
		r.agg.add(rd)
	} else {
		r.agg.missed += rd.Missed
		if rd.HK > r.agg.maxHK {
			r.agg.maxHK = rd.HK
		}
	}
	r.diagBudget(now)

	notable := rd.Err != "" || rd.Missed > 0 || rd.Lag >= diagNotableMin || rd.HK >= diagNotableMin ||
		rd.Total >= diagNotableMin && rd.Total >= interval/2
	for _, g := range rd.Groups {
		// A group that ran out of time or was still busy is news. One that always
		// answers "error" or "empty" (no IIS or SQL on this PC) is not.
		if g.Outcome == "timeout" || g.Outcome == "busy" {
			notable = true
		}
	}
	if notable {
		// At most diagLinesPerMin lines a minute; the rest are only counted.
		if m := now.Truncate(time.Minute); !m.Equal(r.noteMinute) {
			r.noteMinute, r.noteCount = m, 0
		}
		if r.noteCount < diagLinesPerMin {
			r.noteCount++
			d.Line("round", "seq", rd.Seq, "total", rd.Total, "lag", rd.Lag, "missed", rd.Missed,
				"hk", rd.HK, "feed", rd.Feed, "collect", rd.Collect, "host", rd.Host, "append", rd.Append,
				"groups", rd.groupsText(), "err", rd.Err, "paused", paused)
		} else {
			r.agg.suppressed++
		}
	}
	if now.Sub(r.lastBeat) >= diagBeatEvery || now.Before(r.lastBeat) {
		r.diagBeat(now)
	}
}

// diagBeat writes the totals since the last heartbeat and starts the next.
func (r *Recorder) diagBeat(now time.Time) {
	d := r.o.Diag
	a := &r.agg
	avg := time.Duration(0)
	if a.rounds > 0 {
		avg = a.sumTotal / time.Duration(a.rounds)
	}
	base := r.o.Config.IntervalSeconds
	eff := effectiveInterval(r.o.Budget, base)
	r.mu.Lock()
	free, known := r.freePct, r.freeKnown
	r.mu.Unlock()
	freeText := "-"
	if known {
		freeText = diagValue(round1(free))
	}
	d.Line("beat", "rounds", a.rounds, "avg", avg, "max", a.maxTotal, "lagmax", a.maxLag,
		"missed", a.missed, "errs", a.errs, "hkmax", a.maxHK, "appendmax", a.maxAppend,
		"suppressed", a.suppressed, "slow", a.slowText(), "interval", base, "effective", eff,
		"cpu", round1(r.lastSelf.CPUPct), "heapMB", toMB(r.lastSelf.HeapInUse), "privateMB", toMB(r.lastSelf.Private),
		"handles", r.lastSelf.Handles, "free", freeText, "cores", runtime.NumCPU())
	*a = diagAgg{since: now}
	r.lastBeat = now
}

// diagBudget logs what the light budget decided: each finished window with its
// numbers, any change of the interval with the reason, and the groups put on
// probation.
func (r *Recorder) diagBudget(now time.Time) {
	b, d := r.o.Budget, r.o.Diag
	if b == nil || d == nil {
		return
	}
	if seq := b.WindowSeq(); seq != r.lastWinSeq {
		r.lastWinSeq = seq
		w := b.LastWindow()
		kind := map[WindowKind]string{WindowCalm: "calm", WindowBand: "band", WindowOver: "over"}[w.Kind]
		// Calm and band windows are routine; only an over window or a change is written.
		if w.Kind == WindowOver || w.Action != "" {
			d.Line("budget", "window", kind, "reason", w.Reason, "cpu", w.CPUPct, "heapMB", w.HeapMB,
				"privateMB", w.Private, "action", w.Action, "from", w.From, "to", w.To)
		}
	}
	if prob := strings.Join(b.SlowGroups(), ","); prob != r.lastProb {
		r.lastProb = prob
		if prob == "" {
			prob = "none"
		}
		d.Line("budget", "probation", prob)
	}
}

// diagEvent writes an event the manifest also records. detail is already a fixed
// code or a number-only sentence.
func (r *Recorder) diagEvent(kind, detail string) {
	r.o.Diag.Line("event", "kind", kind, "detail", detail)
}

// diagStart and diagStop bracket a run in the log.
func (r *Recorder) diagStart() {
	groups := 0
	if g, ok := r.o.Collector.(interface{ GroupCount() int }); ok {
		groups = g.GroupCount()
	}
	r.o.Diag.Line("start", "run", r.o.RunID, "os", runtime.GOOS, "cores", runtime.NumCPU(),
		"interval", r.o.Config.IntervalSeconds, "effective", effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds),
		"groups", groups, "keepDays", r.o.Config.KeepDays, "maxGB", r.o.Config.MaxGB)
}
