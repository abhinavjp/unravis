package mongoexports

import (
	"encoding/json"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// currentOpAttrs is the allowlist of mongo-currentop. "ns" is listed because the
// case-export policy lists it; the importer never writes it (dropped at parse).
var currentOpAttrs = []string{"op", "ns", "waitingForLock", "numYields", "planSummary"}

// NewCurrentOp returns the importer for a $currentOp snapshot loop (one JSON document
// per active operation per line, as projected by the recipe).
func NewCurrentOp() importer.Importer {
	return impl{
		sp: spec{name: NameCurrentOp, maxEvents: 2_000_000, def: importer.Limits{
			MaxBytes: 256 << 20, MaxRows: 2_000_000, MaxLineBytes: 1 << 20, MaxFields: maxFieldsDflt,
			MaxRejectFraction: rejectFrac}},
		attrs:    currentOpAttrs,
		detect:   detectCurrentOp,
		newLiner: func() liner { return &currentOpLiner{} },
	}
}

func detectCurrentOp(head []byte, fileName string) int {
	line := firstLine(head)
	if line == nil || !hasAll(line, "currentOpTime") {
		return 0
	}
	score := 85
	if hasAny(line, "secs_running", "microsecs_running") {
		score += 5
	}
	if hasAny(line, "waitingForLock", "planSummary", "numYields") {
		score += 3
	}
	if fileHint(fileName, "currentop", "current-op", "current_op") {
		score += 5
	}
	return score
}

type currentOpLiner struct {
	base
	rule string
}

func (c *currentOpLiner) begin(lim importer.Limits, opts importer.Options, rep *importer.Report) error {
	c.setup(lim)
	rep.Rule("currentOpTime", clock.RuleOffsetInField)
	return nil
}

func (c *currentOpLiner) line(line []byte, no int64, _ time.Time, opts importer.Options, rep *importer.Report) (time.Time, []evidence.Event, importer.Reason, error) {
	var f opFields
	var micros, secs float64
	var hasMicros, hasSecs bool
	ok, tooMany, _ := forEachKey(line, c.maxKeys, func(k string, raw json.RawMessage) {
		switch k {
		case "currentOpTime":
			f.ts = raw
		case "op", "type":
			if s, ok := str(raw); ok {
				f.op = s
			}
		case "planSummary":
			if s, ok := str(raw); ok {
				f.plan = stageTokens(s)
			}
		case "waitingForLock":
			if b, ok := boolean(raw); ok {
				f.wait = b
			} else {
				f.bad = true
			}
		case "numYields":
			f.count("numYields", raw)
		case "microsecs_running":
			if n, ok := nonNeg(raw); ok {
				micros, hasMicros = n, true
			} else {
				f.bad = true
			}
		case "secs_running":
			if n, ok := nonNeg(raw); ok {
				secs, hasSecs = n, true
			} else {
				f.bad = true
			}
		case "host":
			if s, ok := str(raw); ok {
				f.host = s
			}
		}
	})
	if !ok || tooMany {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	t, rule, reason := parseMongoTime(f.ts)
	if reason != "" {
		return time.Time{}, nil, reason, nil
	}
	switch {
	case hasMicros:
		f.durMs, f.hasDur = micros/1000, true
	case hasSecs:
		f.durMs, f.hasDur = secs*1000, true
	}
	if f.bad || !f.hasDur || f.durMs > maxDurationMs {
		return time.Time{}, nil, importer.ReasonBadNumber, nil
	}
	label, err := c.host(f.host, opts, rep)
	if err != nil {
		return t, nil, "", err
	}
	c.rule = rule
	rep.Rule("currentOpTime", rule)

	d := f.durMs
	start := t.Add(-time.Duration(d * float64(time.Millisecond)))
	attrs := map[string]string{}
	if o := lower(f.op); currentOps[o] {
		attrs["op"] = o
	}
	if f.wait != "" {
		attrs["waitingForLock"] = f.wait
	}
	if f.plan != "" {
		attrs["planSummary"] = f.plan
	}
	for k, v := range f.counts {
		attrs[k] = v
	}
	ev := newEvent(opts, NameCurrentOp, KindInflightOp, label, no, 0, start, t, rule)
	ev.DurationMs, ev.Unit, ev.Attrs = &d, "ms", attrs
	if err := ev.Validate(set(currentOpAttrs...)); err != nil {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	return t, []evidence.Event{ev}, "", nil
}

func (c *currentOpLiner) end(rep *importer.Report, opts importer.Options) {
	rule := c.rule
	if rule == "" {
		rule = clock.RuleOffsetInField
	}
	rep.Gap("snapshots: operations shorter than the sampling interval are not observed")
	rep.Gap("the snapshot time field is taken as the end of each interval")
	rep.Gap("the file lists only operations the recipe kept (active, running at least the recipe's minimum seconds)")
	rep.Quality = quality(rep, "", rule)
}
