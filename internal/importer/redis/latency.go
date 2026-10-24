package redis

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	latMaxBytes   = 64 << 20
	latMaxRows    = 200_000
	latMaxLine    = 8 << 20
	latMaxMillis  = 1_000_000_000
	latThresholdK = "latency-monitor-threshold"
	maxLatThresh  = int64(1) << 31
)

var latAllowed = map[string]bool{attrEvent: true}

var reLatencyName = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Latency implements importer.Importer for LATENCY HISTORY output, one block per event.
// The owner's script writes "# latency-event: <name>" above each block, because the reply
// itself does not name the event.
type Latency struct{}

// NewLatency returns the redis-latency importer.
func NewLatency() importer.Importer { return Latency{} }

func (Latency) Name() string                  { return NameLatency }
func (Latency) AllowedAttrs() map[string]bool { return copyAttrs(latAllowed) }

func (Latency) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: latMaxBytes, MaxRows: latMaxRows, MaxLineBytes: latMaxLine,
		MaxFields: slowMaxFields, MaxRejectFraction: rejectFrac,
	}
}

func latShape(n *node) bool {
	return n != nil && n.k == kArr && n.total == 2 && len(n.kids) == 2 &&
		n.kids[0].k == kInt && n.kids[1].k == kInt
}

// Detect recognises a reply that is an array of [time, milliseconds] pairs. A
// "# latency-event:" label above it raises the score.
func (Latency) Detect(head []byte, fileName string) int {
	if importer.Sniff(head) != nil {
		return 0
	}
	text := headText(head)
	if text == nil {
		return 0
	}
	p := peek(text, 400)
	if !latShape(p.first) {
		return 0
	}
	score := 85
	if p.label {
		score = 95
	}
	if lower := strings.ToLower(fileName); strings.Contains(lower, "latency") {
		score += 5
	}
	if score > 100 {
		score = 100
	}
	return score
}

type latState struct {
	sink   *sink
	rep    *importer.Report
	opts   importer.Options
	host   string
	event  string
	seen   map[latKey]struct{}
	other  bool
	labels int
}

type latKey struct {
	event string
	ts    int64
	ms    int64
}

// Parse reads the blocks and emits one latency-spike event per [time, milliseconds] pair.
func (Latency) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := capLimits(opts.Limits, Latency{}.DefaultLimits())
	rep := importer.Report{Importer: NameLatency}
	rep.Rule("ts", clock.RuleUTCField)
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	host, err := hostLabel(opts)
	if err != nil {
		return rep, err
	}
	st := &latState{
		sink: &sink{opts: opts, rep: &rep, emit: emit, allowed: latAllowed},
		rep:  &rep, opts: opts, host: host, event: Other,
		seen: map[latKey]struct{}{},
	}
	sc := newScanner(lim, &rep, st.sample, st.label)
	if err := readLines(ctx, r, lim, &rep, sc.feed); err != nil {
		return rep, err
	}
	if err := sc.end(); err != nil {
		return rep, err
	}
	if err := finishRead(&rep, lim); err != nil {
		return rep, err
	}
	st.finish()
	return rep, nil
}

// label reads a "# latency-event: name" comment. The name is used only when it is one of
// the documented event names; anything else is "other".
func (st *latState) label(c []byte) {
	c = bytes.TrimSpace(c)
	if !bytes.HasPrefix(c, []byte(commentLabel)) {
		return
	}
	name := string(bytes.TrimSpace(c[len(commentLabel):]))
	st.event = Other
	if reLatencyName.MatchString(name) {
		st.event = canonicalLatencyEvent(name)
	}
	st.labels++
}

func (st *latState) sample(n *node) error {
	rep := st.rep
	if !latShape(n) {
		rep.Reject(n.line, importer.ReasonFieldCount)
		return nil
	}
	ts, ms := n.kids[0].n, n.kids[1].n
	at, ok := unixTime(ts)
	if !ok {
		rep.Reject(n.line, importer.ReasonBadTime)
		return nil
	}
	if ms < 0 || ms > latMaxMillis {
		rep.Reject(n.line, importer.ReasonBadNumber)
		return nil
	}
	v := float64(ms)
	ev := evidence.Event{
		EvidenceID: evidence.EventID(st.opts.CaseID, NameLatency, st.opts.ImportNonce, n.line, 0),
		Source:     NameLatency,
		Kind:       KindLatencySpike,
		Host:       st.host,
		Start:      at,
		End:        at,
		Value:      &v,
		Unit:       "ms",
		Line:       n.line,
		TZRule:     clock.RuleUTCField,
		Attrs:      map[string]string{attrEvent: st.event},
	}
	if ev.Validate(latAllowed) != nil {
		rep.Reject(n.line, importer.ReasonFieldCount)
		return nil
	}
	if st.event == Other {
		st.other = true
	}
	rep.Cover(at)
	key := latKey{st.event, ts, ms}
	if _, dup := st.seen[key]; dup {
		return nil
	}
	st.seen[key] = struct{}{}
	_, err := st.sink.send(ev)
	return err
}

func (st *latState) finish() {
	rep, opts := st.rep, st.opts
	q := evidence.SourceQuality{}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
		q.TimezoneRule = clock.RuleUTCField
	}
	rep.Gap("LATENCY HISTORY keeps at most 160 samples per event, one per second; older samples are discarded")
	rep.Gap("The latency monitor records only events at or above latency-monitor-threshold; shorter events are not in this file")
	if v, ok := suppliedInt(opts.Supplied[latThresholdK], maxLatThresh); ok && v > 0 {
		q.Threshold = fmt.Sprintf("events of at least %d ms", v)
	} else {
		q.Threshold = "unknown"
		rep.Gap("latency-monitor-threshold unknown: not supplied by the owner")
	}
	if st.other {
		rep.Gap("A block had no latency-event label or an event name outside the documented list; it is shown as other")
	}
	rep.Quality = q
}
