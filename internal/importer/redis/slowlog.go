package redis

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	slowMaxBytes    = 64 << 20
	slowMaxRows     = 200_000
	slowMaxLine     = 8 << 20
	slowMaxFields   = 64
	rejectFrac      = 0.05
	maxSlowID       = 1e15
	maxSlowMicros   = int64(400 * 24 * 3600 * 1_000_000) // 400 days
	maxArgc         = 1_000_000_000
	maxClientLen    = 64
	defaultSlowMax  = 128
	slowThresholdK  = "slowlog-log-slower-than"
	slowMaxLenK     = "slowlog-max-len"
	suppliedExpAtK  = "exported-at"
	maxSuppliedLen  = 32
	maxSlowThreshUS = int64(1) << 40
)

var slowAllowed = map[string]bool{attrCommand: true, attrArgc: true, attrClient: true, attrID: true}

var reMoreArgs = regexp.MustCompile(`^\.\.\. \(([0-9]{1,12}) more arguments?\)$`)

// Slowlog implements importer.Importer for SLOWLOG GET output.
type Slowlog struct{}

// NewSlowlog returns the redis-slowlog importer.
func NewSlowlog() importer.Importer { return Slowlog{} }

func (Slowlog) Name() string                  { return NameSlowlog }
func (Slowlog) AllowedAttrs() map[string]bool { return copyAttrs(slowAllowed) }

func (Slowlog) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: slowMaxBytes, MaxRows: slowMaxRows, MaxLineBytes: slowMaxLine,
		MaxFields: slowMaxFields, MaxRejectFraction: rejectFrac,
	}
}

func slowShape(n *node) bool {
	return n != nil && n.k == kArr && len(n.kids) >= 4 &&
		n.kids[0].k == kInt && n.kids[1].k == kInt && n.kids[2].k == kInt && n.kids[3].k == kArr
}

// Detect recognises a SLOWLOG GET reply: an array of entries, each with an id, a time, a
// duration and an argument array.
func (Slowlog) Detect(head []byte, fileName string) int {
	if importer.Sniff(head) != nil {
		return 0
	}
	text := headText(head)
	if text == nil {
		return 0
	}
	p := peek(text, 400)
	if !slowShape(p.first) {
		return 0
	}
	score := 85
	lower := strings.ToLower(fileName)
	if strings.Contains(lower, "slowlog") || strings.Contains(lower, "redis") {
		score += 5
	}
	return score
}

type slowState struct {
	sink       *sink
	rep        *importer.Report
	opts       importer.Options
	host       string
	seen       map[[3]int64]struct{}
	ids        map[int64]struct{}
	exportedAt time.Time
	any        bool
	ordinal    int
}

// Parse reads the reply and emits one slow-command event per entry. Command arguments,
// keys and client addresses are dropped here; see the package comment.
func (Slowlog) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := capLimits(opts.Limits, Slowlog{}.DefaultLimits())
	rep := importer.Report{Importer: NameSlowlog}
	rep.Rule("ts", clock.RuleUTCField)
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	host, err := hostLabel(opts)
	if err != nil {
		return rep, err
	}
	st := &slowState{
		sink: &sink{opts: opts, rep: &rep, emit: emit, allowed: slowAllowed},
		rep:  &rep, opts: opts, host: host,
		seen: map[[3]int64]struct{}{}, ids: map[int64]struct{}{},
	}
	sc := newScanner(lim, &rep, st.entry, func(c []byte) {
		if t, ok := sampleTime(c); ok && t.After(st.exportedAt) {
			st.exportedAt = t.UTC()
		}
	})
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

// entry handles one element of the reply.
func (st *slowState) entry(n *node) error {
	rep := st.rep
	if !slowShape(n) || len(n.kids[3].kids) == 0 || n.kids[3].kids[0].k != kStr {
		rep.Reject(n.line, importer.ReasonFieldCount)
		return nil
	}
	id, ts, us := n.kids[0].n, n.kids[1].n, n.kids[2].n
	if id < 0 || float64(id) >= maxSlowID || us < 0 || us >= maxSlowMicros {
		rep.Reject(n.line, importer.ReasonBadNumber)
		return nil
	}
	end, ok := unixTime(ts)
	if !ok {
		rep.Reject(n.line, importer.ReasonBadTime)
		return nil
	}
	args := n.kids[3]
	argc := int64(args.total)
	if m := reMoreArgs.FindStringSubmatch(args.last); m != nil {
		more, _ := strconv.ParseInt(m[1], 10, 64)
		argc = argc - 1 + more
	}
	if argc < 1 || argc > maxArgc {
		rep.Reject(n.line, importer.ReasonBadNumber)
		return nil
	}

	d := float64(us) / 1000
	start := end.Add(-time.Duration(us) * time.Microsecond)
	attrs := map[string]string{
		attrCommand: CanonicalCommand(args.kids[0].s),
		attrArgc:    strconv.FormatInt(argc, 10),
		attrID:      strconv.FormatInt(id, 10),
	}
	if len(n.kids) > 5 && n.kids[5].k == kStr && n.kids[5].s != "" && st.opts.Pseudonymize != nil {
		name := strings.ToValidUTF8(n.kids[5].s, "")
		if p := st.opts.Pseudonymize("client", name); p != "" && len(p) <= maxClientLen && utf8.ValidString(p) {
			attrs[attrClient] = p
		}
	}
	st.ordinal++
	ev := evidence.Event{
		EvidenceID: evidence.EventID(st.opts.CaseID, NameSlowlog, st.opts.ImportNonce, n.line, st.ordinal),
		Source:     NameSlowlog,
		Kind:       KindSlowCommand,
		Host:       st.host,
		Start:      start,
		End:        end,
		DurationMs: &d,
		Unit:       "ms",
		Severity:   "info",
		Line:       n.line,
		TZRule:     clock.RuleUTCField,
		Attrs:      attrs,
	}
	if ev.Validate(slowAllowed) != nil {
		rep.Reject(n.line, importer.ReasonFieldCount)
		return nil
	}
	st.any = true
	rep.Cover(start)
	rep.Cover(end)
	st.ids[id] = struct{}{}
	key := [3]int64{id, ts, us}
	if _, dup := st.seen[key]; dup {
		return nil // the same entry read in an earlier snapshot
	}
	st.seen[key] = struct{}{}
	_, err := st.sink.send(ev)
	return err
}

func (st *slowState) finish() {
	rep, opts := st.rep, st.opts
	exp := st.exportedAt
	if s := strings.TrimSpace(opts.Supplied[suppliedExpAtK]); s != "" && len(s) <= 64 {
		if t, err := time.Parse(time.RFC3339, s); err == nil && t.Year() >= 2000 && t.Year() <= 2100 && t.After(exp) {
			exp = t.UTC()
		}
	}
	if !exp.IsZero() {
		rep.Cover(exp)
	}

	q := evidence.SourceQuality{}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
		q.TimezoneRule = clock.RuleUTCField
	}

	rep.Gap("SLOWLOG records only commands slower than slowlog-log-slower-than; faster commands are not in this file")
	rep.Gap("SLOWLOG measures command execution only; network, queueing and I/O time are not included")
	if v, ok := suppliedInt(opts.Supplied[slowThresholdK], maxSlowThreshUS); ok {
		q.Threshold = fmt.Sprintf("commands slower than %d microseconds", v)
	} else {
		q.Threshold = "unknown"
		rep.Gap("slowlog-log-slower-than unknown: not supplied by the owner")
	}
	if exp.IsZero() {
		rep.Gap("Export time unknown: the file does not show how long after the newest entry the SLOWLOG was read")
	}

	maxLen := int64(defaultSlowMax)
	if v, ok := suppliedInt(opts.Supplied[slowMaxLenK], 1<<20); ok && v > 0 {
		maxLen = v
	}
	if st.any && (int64(len(st.ids)) >= maxLen || idGap(st.ids)) {
		rep.Gap("SLOWLOG is a ring buffer of fixed length; entries older than the oldest one in this file may have been overwritten")
	}
	rep.Quality = q
}

func idGap(ids map[int64]struct{}) bool {
	s := make([]int64, 0, len(ids))
	for id := range ids {
		s = append(s, id)
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	for i := 1; i < len(s); i++ {
		if s[i]-s[i-1] != 1 {
			return true
		}
	}
	return false
}

// suppliedInt reads an owner-supplied whole number in 0..max.
func suppliedInt(s string, max int64) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxSuppliedLen || bytes.IndexByte([]byte(s), '+') >= 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || v > max {
		return 0, false
	}
	return v, true
}
