package mongoexports

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Names of the four importers and the kinds they emit.
const (
	NameProfile      = "mongo-profile"
	NameCurrentOp    = "mongo-currentop"
	NameServerStatus = "mongo-serverstatus"
	NameMongostat    = "mongostat-json"

	KindSlowOp      = "slow-op"
	KindInflightOp  = "inflight-op"
	KindMetric      = "metric"
	KindMongodStart = "mongod-start"
)

// profileAttrs is the attribute allowlist of mongo-profile. "ns" is listed because the
// case-export policy lists it, but this importer never writes it: the namespace is
// dropped at parse.
var profileAttrs = []string{"type", "ns", "command", "planSummary", "keysExamined", "docsExamined", "nreturned",
	"numYields", "queryHash", "workingMs", "responseLength"}

// NewProfile returns the importer for a system.profile export (mongoexport, one JSON
// document per line).
func NewProfile() importer.Importer {
	return impl{
		sp: spec{name: NameProfile, maxEvents: 2_000_000, def: importer.Limits{
			MaxBytes: 1 << 30, MaxRows: 2_000_000, MaxLineBytes: 1 << 20, MaxFields: maxFieldsDflt,
			MaxRejectFraction: rejectFrac}},
		attrs:    profileAttrs,
		detect:   detectProfile,
		newLiner: func() liner { return &profileLiner{} },
	}
}

// detectProfile scores a first line that has the profiler's own key set: op, millis and
// ts, and neither a currentOp time nor a serverStatus time.
func detectProfile(head []byte, fileName string) int {
	line := firstLine(head)
	if line == nil || !hasAll(line, "op", "millis", "ts") || hasAny(line, "currentOpTime", "localTime") {
		return 0
	}
	score := 85
	if hasAny(line, "planSummary", "docsExamined", "keysExamined") {
		score += 5
	}
	if fileHint(fileName, "profile") {
		score += 5
	}
	return score
}

type profileLiner struct {
	base
	rule string
}

func (p *profileLiner) begin(lim importer.Limits, opts importer.Options, rep *importer.Report) error {
	p.setup(lim)
	rep.Rule("ts", clock.RuleOffsetInField)
	return nil
}

// opFields is what one profile or currentOp document leaves after the parse. Nothing
// else of the line is kept: names, filters, commands and addresses are dropped.
type opFields struct {
	op, cmd, plan, qhash, host string
	wait                       string
	counts                     map[string]string
	ts                         json.RawMessage
	durMs                      float64
	hasDur                     bool
	bad                        bool
}

func (f *opFields) count(key string, raw json.RawMessage) {
	if f.counts == nil {
		f.counts = map[string]string{}
	}
	v, ok := count(raw)
	if !ok {
		f.bad = true
		return
	}
	f.counts[key] = v
}

func (p *profileLiner) line(line []byte, no int64, _ time.Time, opts importer.Options, rep *importer.Report) (time.Time, []evidence.Event, importer.Reason, error) {
	var f opFields
	ok, tooMany, _ := forEachKey(line, p.maxKeys, func(k string, raw json.RawMessage) {
		switch k {
		case "op", "type":
			if s, ok := str(raw); ok {
				f.op = s
			}
		case "command":
			f.cmd = firstKey(raw)
		case "planSummary":
			if s, ok := str(raw); ok {
				f.plan = stageTokens(s)
			}
		case "queryHash":
			if s, ok := str(raw); ok && reQHash.MatchString(s) {
				f.qhash = s
			}
		case "keysExamined", "docsExamined", "nreturned", "responseLength":
			f.count(k, raw)
		case "numYield", "numYields":
			f.count("numYields", raw)
		case "workingMillis":
			if n, ok := nonNeg(raw); ok && n <= maxDurationMs {
				f.count2("workingMs", n)
			} else {
				f.bad = true
			}
		case "millis", "durationMillis":
			if n, ok := nonNeg(raw); ok && n <= maxDurationMs {
				f.durMs, f.hasDur = n, true
			} else {
				f.bad = true
			}
		case "ts":
			f.ts = raw
		}
	})
	if !ok {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	if tooMany {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	end, rule, reason := parseMongoTime(f.ts)
	if reason != "" {
		return time.Time{}, nil, reason, nil
	}
	if f.bad || !f.hasDur {
		return time.Time{}, nil, importer.ReasonBadNumber, nil
	}
	label, err := p.host("", opts, rep)
	if err != nil {
		return end, nil, "", err
	}
	p.rule = rule
	rep.Rule("ts", rule)

	d := f.durMs
	start := end.Add(-time.Duration(d * float64(time.Millisecond)))
	attrs := map[string]string{}
	if t := lower(f.op); profileTypes[t] {
		attrs["type"] = t
	}
	if f.cmd != "" {
		attrs["command"] = f.cmd
	}
	if f.plan != "" {
		attrs["planSummary"] = f.plan
	}
	for k, v := range f.counts {
		attrs[k] = v
	}
	if f.qhash != "" {
		attrs["queryHash"] = f.qhash
	}
	ev := newEvent(opts, NameProfile, KindSlowOp, label, no, 0, start, end, rule)
	ev.DurationMs, ev.Unit, ev.Attrs = &d, "ms", attrs
	if err := ev.Validate(set(profileAttrs...)); err != nil {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	return end, []evidence.Event{ev}, "", nil
}

func (f *opFields) count2(key string, n float64) {
	if f.counts == nil {
		f.counts = map[string]string{}
	}
	f.counts[key] = fmtNum(n)
}

func lower(s string) string { return string(bytes.ToLower([]byte(s))) }

func (p *profileLiner) end(rep *importer.Report, opts importer.Options) {
	rule := p.rule
	if rule == "" {
		rule = clock.RuleOffsetInField
	}
	q := quality(rep, "", rule)
	if v, ok := slowmsThreshold(opts); ok {
		q.Threshold = "operations slower than " + strconv.FormatInt(v, 10) + " ms"
	} else {
		rep.Gap("slowms unknown: not supplied by the owner")
	}
	rep.Gap("profiler level and filter unknown unless stated by the owner")
	rep.Gap("system.profile is a capped collection: older operations may have been overwritten")
	rep.Gap("the profile time field is read as the end of each operation")
	rep.Quality = q
}
