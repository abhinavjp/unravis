package server

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/casestore"
)

// The charts view reads what a Blackbox recorded and answers it as downsampled
// series, one per measurement. Nothing is collected here and nothing is written:
// the samples come from the same reader as the host reading. Every numeric value
// of every group is charted by walking the sample itself, so a measurement the
// recorder adds later shows up without a change here. A value that was not read
// is absent (never 0), so a chart line breaks where the recorder had no reading.

const (
	bbSeriesDefaultPoints = 240
	bbSeriesMaxPoints     = 600
	bbSeriesMaxProcs      = 8
	bbSeriesMaxEventIDs   = 20
	bbSeriesMaxDumps      = 50
	bbSeriesMaxKeys       = 400
)

// bbSeriesGroups are the groups a sample can carry, in the order they are shown.
var bbSeriesGroups = []string{"http", "iis", "diskCpu", "sqlHost", "clr", "proc10", "procIO", "events", "tcp", "aspnet", "memPressure", "vm", "dumps"}

type bbSeriesBody struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Points int       `json:"points"`
	// Width is the chart width in pixels (BL-430, optional). With it the answer
	// holds at most min(Width, bbSeriesMaxPoints) buckets: at most four values
	// per pixel once first and last are asked for (Shape "m4"). Without it the
	// answer is cut to Points, as before.
	Width int `json:"width"`
	// Shape is "" (the points are [t, avg, lo, hi]) or "m4" (the points are
	// [t, avg, lo, hi, first, last]).
	Shape string `json:"shape"`
}

// bbSeriesPoint is [bucket start in ms, average, lowest, highest].
type bbSeriesPoint [4]float64

// bbSeriesPointM4 is [bucket start in ms, average, lowest, highest, first, last]:
// the lowest and highest are the whole bucket's, the first and last are by time.
type bbSeriesPointM4 [6]float64

type bbSeriesPointRef struct {
	N int `json:"n"`
}

type bbSeriesLine struct {
	Key    string          `json:"key"`   // for example "memCommit" or "sqlHost.SQLEXPRESS.batchRequestsPerSec"
	Group  string          `json:"group"` // "host" for the core measurements, else the group name
	Label  string          `json:"label"`
	Unit   string          `json:"unit"` // "pct", "bytes", "bytesPerSec", "ms", "sec", "perSec", "count"
	Points []bbSeriesPoint `json:"points"`
	// M4, when set (Shape "m4"), is what the answer carries as "points" instead.
	M4     []bbSeriesPointM4 `json:"-"`
	Binary bool              `json:"-"`
}

func (l bbSeriesLine) MarshalJSON() ([]byte, error) {
	type plain bbSeriesLine
	if l.Binary {
		n := len(l.Points) + len(l.M4)
		return json.Marshal(struct {
			plain
			Points bbSeriesPointRef `json:"points"`
		}{plain(l), bbSeriesPointRef{N: n}})
	}
	if l.M4 == nil {
		return json.Marshal(plain(l))
	}
	return json.Marshal(struct {
		plain
		Points []bbSeriesPointM4 `json:"points"`
	}{plain(l), l.M4})
}

type bbSeriesGroupCover struct {
	Group   string `json:"group"`
	Read    int    `json:"read"`    // samples that carried a reading
	Missing int    `json:"missing"` // samples that said it could not be read
	Slow    int    `json:"slow"`    // skipped by the light budget
	Busy    int    `json:"busy"`    // previous reading still running
	// Partial counts samples whose reading covered only part of what it sums
	// (a flag in bbPartialFlags was set: some network adapters unreadable or over the cap; BL-138).
	Partial int `json:"partial"`
}

type bbSeriesProc struct {
	Name   string          `json:"name"`
	PeakCP float64         `json:"peakCpuPct"`
	CPU    []bbSeriesPoint `json:"cpu"`
	WS     []bbSeriesPoint `json:"workingSet"`
	Priv   []bbSeriesPoint `json:"privateBytes"`
	// With Shape "m4" these (M4On set) are answered as cpu, workingSet and privateBytes instead.
	M4On                bool              `json:"-"`
	M4CPU, M4WS, M4Priv []bbSeriesPointM4 `json:"-"`
	Binary              bool              `json:"-"`
}

func (p bbSeriesProc) MarshalJSON() ([]byte, error) {
	type plain bbSeriesProc
	if p.Binary {
		return json.Marshal(struct {
			plain
			CPU  bbSeriesPointRef `json:"cpu"`
			WS   bbSeriesPointRef `json:"workingSet"`
			Priv bbSeriesPointRef `json:"privateBytes"`
		}{plain(p), bbSeriesPointRef{N: len(p.CPU) + len(p.M4CPU)}, bbSeriesPointRef{N: len(p.WS) + len(p.M4WS)}, bbSeriesPointRef{N: len(p.Priv) + len(p.M4Priv)}})
	}
	if !p.M4On {
		return json.Marshal(plain(p))
	}
	return json.Marshal(struct {
		plain
		CPU  []bbSeriesPointM4 `json:"cpu"`
		WS   []bbSeriesPointM4 `json:"workingSet"`
		Priv []bbSeriesPointM4 `json:"privateBytes"`
	}{plain(p), p.M4CPU, p.M4WS, p.M4Priv})
}

type bbSeriesEventID struct {
	Channel uint8  `json:"channel"`
	EventID uint32 `json:"eventId"`
	Level   uint8  `json:"level"`
	Count   uint32 `json:"count"`
}

type bbSeriesDump struct {
	Name   string    `json:"name"`
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Target string    `json:"target,omitempty"`
}

type bbSeriesGap struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Seconds int       `json:"seconds"`
	Cause   string    `json:"cause"`
}

type bbSeriesMarker struct {
	T      time.Time `json:"t"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

type bbSeriesAnswer struct {
	Start           time.Time            `json:"start"`
	End             time.Time            `json:"end"`
	BucketSeconds   int                  `json:"bucketSeconds"`
	IntervalSeconds int                  `json:"intervalSeconds"`
	Samples         int                  `json:"samples"`
	First           *time.Time           `json:"first,omitempty"`
	Last            *time.Time           `json:"last,omitempty"`
	Lines           []bbSeriesLine       `json:"lines"`
	Groups          []bbSeriesGroupCover `json:"groups"`
	Processes       []bbSeriesProc       `json:"processes"`
	EventIDs        []bbSeriesEventID    `json:"eventIds"`
	Dumps           []bbSeriesDump       `json:"dumps"`
	Gaps            []bbSeriesGap        `json:"gaps"`
	Markers         []bbSeriesMarker     `json:"markers"`
	Read            bbReadSummary        `json:"read"`
	State           blackbox.StateInfo   `json:"state"`
	Truncated       bool                 `json:"truncated,omitempty"` // more measurements than the answer carries
	// SQL says what the SQL service's part of the answer rests on (absent when it was not asked for).
	SQL *bbSeriesSQL `json:"sql,omitempty"`
}

// bbAcc is the sum, lowest, highest and count of one bucket of one measurement.
type bbAcc struct {
	sum, lo, hi float64
	n           int
}

func (a *bbAcc) add(v float64) {
	if a.n == 0 {
		a.lo, a.hi = v, v
	} else {
		a.lo, a.hi = math.Min(a.lo, v), math.Max(a.hi, v)
	}
	a.sum += v
	a.n++
}

// bbBuckets collects values per key and time bucket. With m4 set it also keeps
// the first and last value of every bucket by time (BL-430), in edges, so the
// default answer pays nothing for them.
type bbBuckets struct {
	start  time.Time
	width  time.Duration
	n      int
	byKey  map[string][]bbAcc
	maxKey int
	over   bool
	m4     bool
	edges  map[string][]casestore.M4Edges
}

func (b *bbBuckets) add(key string, t time.Time, v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	i := int(t.Sub(b.start) / b.width)
	if i < 0 || i >= b.n {
		return
	}
	row, ok := b.byKey[key]
	if !ok {
		if len(b.byKey) >= b.maxKey {
			b.over = true
			return
		}
		row = make([]bbAcc, b.n)
		b.byKey[key] = row
		if b.m4 {
			b.edges[key] = make([]casestore.M4Edges, b.n)
		}
	}
	row[i].add(v)
	if b.m4 {
		b.edges[key][i].Add(t.UnixNano(), v)
	}
}

func (b *bbBuckets) points(key string) []bbSeriesPoint {
	row := b.byKey[key]
	out := make([]bbSeriesPoint, 0, len(row))
	for i, a := range row {
		if a.n == 0 {
			continue
		}
		out = append(out, bbSeriesPoint{float64(b.start.Add(time.Duration(i) * b.width).UnixMilli()), a.sum / float64(a.n), a.lo, a.hi})
	}
	return out
}

// pointsM4 is points with the first and last value of each bucket added.
func (b *bbBuckets) pointsM4(key string) []bbSeriesPointM4 {
	row, edges := b.byKey[key], b.edges[key]
	out := make([]bbSeriesPointM4, 0, len(row))
	for i, a := range row {
		if a.n == 0 {
			continue
		}
		out = append(out, bbSeriesPointM4{float64(b.start.Add(time.Duration(i) * b.width).UnixMilli()), a.sum / float64(a.n), a.lo, a.hi, edges[i].First, edges[i].Last})
	}
	return out
}

var bbNameFields = []string{"pool", "instance", "target", "name"}

var bbTCPStateNames = map[int]string{1: "closed", 2: "listen", 3: "syn-sent", 4: "syn-received", 5: "established", 6: "fin-wait-1", 7: "fin-wait-2", 8: "close-wait", 9: "closing", 10: "last-ack", 11: "time-wait", 12: "delete-tcb"}

// bbSeriesSkip are fields that are not measurements: identities, flags and the
// parts that have their own place in the answer.
var bbSeriesSkip = map[string]bool{"v": true, "run": true, "seq": true, "t": true, "procs": true, "logs": true, "missing": true, "events": true, "dumps": true, "pid": true, "startTicks": true, "start": true, "channel": true, "eventId": true, "level": true,
	// Counts of what a sum covered, not measurements (BL-146).
	"outboundQueueInterfaces": true, "discardedInterfaces": true, "outErrorsInterfaces": true}

// walk adds every numeric value under v to the buckets. Lists of named things
// (pools, instances, targets, disks) are keyed by their name; TCP states by the
// state's name.
func (b *bbBuckets) walk(prefix string, t time.Time, v any) {
	switch x := v.(type) {
	case float64:
		b.add(prefix, t, x)
	case map[string]any:
		for k, child := range x {
			if bbSeriesSkip[k] {
				continue
			}
			if _, isList := child.([]any); isList && k != "states" {
				// A list of named things: the name is the key, the list's own word is dropped.
				b.walk(prefix, t, child)
				continue
			}
			b.walk(bbJoin(prefix, k), t, child)
		}
	case []any:
		for _, item := range x {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := ""
			for _, f := range bbNameFields {
				if s, ok := obj[f].(string); ok && s != "" {
					name = s
					break
				}
			}
			if name == "" {
				if st, ok := obj["state"].(float64); ok {
					n := bbTCPStateNames[int(st)]
					if n == "" {
						n = "state-" + strconv.Itoa(int(st))
					}
					if c, ok := obj["count"].(float64); ok {
						b.add(bbJoin(prefix, n), t, c)
					}
				}
				continue
			}
			for k, child := range obj {
				if k == "pool" || k == "instance" || k == "target" || k == "name" || bbSeriesSkip[k] {
					continue
				}
				b.walk(bbJoin(bbJoin(prefix, name), k), t, child)
			}
		}
	}
}

// bbPartialFlags are the flags that say a sum covered only some of what it adds up. Other "truncated" flags
// (delayed events, a long dump list) do not make a total low, so they are not listed.
var bbPartialFlags = map[string]bool{"outboundQueuePartial": true, "discardedPartial": true, "outErrorsPartial": true}

// bbPartial is true when any of bbPartialFlags is set anywhere under v.
func bbPartial(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if b, ok := child.(bool); ok && b && bbPartialFlags[k] {
				return true
			}
			if bbPartial(child) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if bbPartial(item) {
				return true
			}
		}
	}
	return false
}

func bbJoin(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

func bbSeriesUnit(key string) string {
	last := key[strings.LastIndex(key, ".")+1:]
	l := strings.ToLower(last)
	// Fields whose name does not say their unit (BL-146).
	switch last {
	case "pctTimeInGC":
		return "pct"
	case "maxQueueItemAge":
		return "ms"
	case "pageLifeExpectancy", "elapsedSec":
		return "sec"
	}
	switch {
	case strings.HasSuffix(l, "bytespersec"):
		return "bytesPerSec"
	case strings.HasSuffix(l, "persec"):
		return "perSec"
	case strings.HasSuffix(l, "pct") || l == "cpu" || l == "diskfreepct":
		return "pct"
	case strings.HasSuffix(l, "bytes") || l == "memavail" || l == "memcommit" || l == "workingset":
		return "bytes"
	case strings.HasSuffix(last, "Ms"): // case-sensitive: "items" is a count, "diskMs" is time
		return "ms"
	case l == "netinbps" || l == "netoutbps":
		return "bytesPerSec"
	}
	return "count"
}

func bbSeriesGroupOf(key string) string {
	i := strings.Index(key, ".")
	if i < 0 {
		return "host"
	}
	return key[:i]
}

func (h *handler) bbSeries(w http.ResponseWriter, r *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	b := h.bb
	var body bbSeriesBody
	if !decodeJSON(w, r, &body) {
		return
	}
	win := bbWindow{Start: body.Start.UTC(), End: body.End.UTC()}
	if !win.valid() {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	if body.Shape != "" && body.Shape != "m4" || body.Width < 0 {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	points := bbSeriesPointCount(body.Points, body.Width)
	e, err := b.deps.Store.Get(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	rd, samples, rep, err := b.readSamples(r.Context(), e, win.engine())
	if err != nil {
		st := blackbox.DeriveState(blackbox.StateInput{Placed: true, Err: err})
		failFromLook(w, bbLook{Err: err, State: st})
		return
	}
	rp := rep
	state := blackbox.DeriveState(blackbox.StateInput{Placed: true, Reading: &rd, Report: &rp})
	sqlRead := b.readSQLSamples(r.Context(), e, win)
	ans := buildBBSeriesWith(rd.Manifest, samples, rep, state, win, points, body.Shape == "m4", body.Width > 0 || body.Shape == "m4", sqlRead)
	if body.Shape == "m4" && strings.Contains(r.Header.Get("Accept"), bbSeriesBinaryType) {
		writeBBSeriesBinary(w, ans)
		return
	}
	writeJSON(w, http.StatusOK, ans, maxResponseBytes)
}

// bbSeriesPointCount is the number of buckets to answer: the asked points (the
// default when none), cut to the chart width in pixels when one is given, and
// never above bbSeriesMaxPoints. A width alone asks for one bucket per pixel.
func bbSeriesPointCount(points, width int) int {
	if points <= 0 {
		points = bbSeriesDefaultPoints
		if width > 0 {
			points = width
		}
	}
	if width > 0 && points > width {
		points = width
	}
	if points > bbSeriesMaxPoints {
		points = bbSeriesMaxPoints
	}
	return points
}

func buildBBSeries(m blackbox.Manifest, samples []blackbox.Sample, rep blackbox.ReadReport, state blackbox.StateInfo, win bbWindow, points int) bbSeriesAnswer {
	return buildBBSeriesShape(m, samples, rep, state, win, points, false, false)
}

// buildBBSeriesShape is buildBBSeries; with m4 set every point also carries the
// first and last value of its bucket (BL-430). With bound set the answer never
// holds more than points buckets (the default answer holds one more when the
// window divides evenly, and stays as it was).
func buildBBSeriesShape(m blackbox.Manifest, samples []blackbox.Sample, rep blackbox.ReadReport, state blackbox.StateInfo, win bbWindow, points int, m4, bound bool) bbSeriesAnswer {
	return buildBBSeriesWith(m, samples, rep, state, win, points, m4, bound, nil)
}

// buildBBSeriesWith is buildBBSeriesShape with the SQL service's samples for the same window
// (nil when they were not read: the answer then has no "sql" part and no sqlDmv lines).
func buildBBSeriesWith(m blackbox.Manifest, samples []blackbox.Sample, rep blackbox.ReadReport, state blackbox.StateInfo, win bbWindow, points int, m4, bound bool, sqlIn *bbSQLRead) bbSeriesAnswer {
	width := win.End.Sub(win.Start) / time.Duration(points)
	if bound {
		width++ // span/width + 1 <= points, as M4BucketMs does for the case series
	}
	// A point is never narrower than the recorder's real cadence (it may be slowed to keep the PC light),
	// so a normal reading is one bucket and a line only breaks where readings are really missing.
	if floor := time.Duration(m.CadenceSeconds()) * time.Second; width < floor {
		width = floor
	}
	if width < time.Second {
		width = time.Second
	}
	n := int(win.End.Sub(win.Start)/width) + 1
	bk := &bbBuckets{start: win.Start, width: width, n: n, byKey: map[string][]bbAcc{}, maxKey: bbSeriesMaxKeys, m4: m4, edges: map[string][]casestore.M4Edges{}}
	procBk := &bbBuckets{start: win.Start, width: width, n: n, byKey: map[string][]bbAcc{}, maxKey: bbSeriesMaxKeys * 3, m4: m4, edges: map[string][]casestore.M4Edges{}}
	cover := map[string]*bbSeriesGroupCover{}
	for _, g := range bbSeriesGroups {
		cover[g] = &bbSeriesGroupCover{Group: g}
	}
	ids := map[[3]uint32]uint32{}
	dumps := map[string]bbSeriesDump{}
	var prev *blackbox.Sample
	var times []time.Time
	for i := range samples {
		s := samples[i]
		times = append(times, s.T)
		raw, err := json.Marshal(s)
		if err != nil {
			continue
		}
		var tree map[string]any
		if json.Unmarshal(raw, &tree) != nil {
			continue
		}
		for _, g := range bbSeriesGroups {
			if sub, ok := tree[g]; ok {
				cover[g].Read++
				if bbPartial(sub) {
					cover[g].Partial++
				}
			}
		}
		for _, miss := range s.Missing {
			name, why, _ := strings.Cut(miss, ":")
			c := cover[strings.TrimSpace(name)]
			if c == nil {
				continue
			}
			switch strings.TrimSpace(why) {
			case "slow":
				c.Slow++
			case "busy":
				c.Busy++
			default:
				c.Missing++
			}
		}
		// Cumulative counters become a rate between two samples of the same run.
		rate := func(key string, cur, was *uint64) {
			if cur == nil || was == nil || prev == nil || *cur < *was {
				return
			}
			dt := s.T.Sub(prev.T).Seconds()
			if dt > 0 {
				bk.add(key, s.T, float64(*cur-*was)/dt)
			}
		}
		if prev != nil && prev.Run == s.Run {
			rate("netInBps", s.NetIn, prev.NetIn)
			rate("netOutBps", s.NetOut, prev.NetOut)
		}
		prev = &samples[i]
		delete(tree, "netIn")
		delete(tree, "netOut")
		bk.walk("", s.T, tree)
		if s.Events != nil {
			for _, ev := range s.Events.Events {
				ids[[3]uint32{uint32(ev.Channel), ev.EventID, uint32(ev.Level)}] += ev.Count
				bk.add("events.level"+strconv.Itoa(int(ev.Level)), ev.T, float64(ev.Count))
			}
			bk.add("events.lost", s.T, float64(s.Events.Lost))
			bk.add("events.unreadable", s.T, float64(s.Events.Unreadable))
		}
		if s.Dumps != nil {
			for _, d := range s.Dumps.Dumps {
				// The collector lists the folder's newest dumps on every scan; only those written inside the window belong here.
				if d.MTime.Before(win.Start) || d.MTime.After(win.End) {
					continue
				}
				dumps[d.Name] = bbSeriesDump{Name: d.Name, Size: d.Size, MTime: d.MTime.UTC(), Target: d.Target}
			}
		}
		for _, p := range s.Procs {
			if p.CPUPct != nil {
				procBk.add("p."+p.Name+".cpu", s.T, *p.CPUPct)
			}
			if p.WS != nil {
				procBk.add("p."+p.Name+".ws", s.T, float64(*p.WS))
			}
			if p.Private != nil {
				procBk.add("p."+p.Name+".priv", s.T, float64(*p.Private))
			}
		}
	}

	if sqlIn != nil {
		// The SQL service's part is not a group of the host sample: it has its own cover, only when it was read.
		cover[bbDmvGroup] = &bbSeriesGroupCover{Group: bbDmvGroup}
	}
	addSQLSeries(bk, cover, sqlIn)

	keys := make([]string, 0, len(bk.byKey))
	for k := range bk.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]bbSeriesLine, 0, len(keys))
	for _, k := range keys {
		line := bbSeriesLine{Key: k, Group: bbSeriesGroupOf(k), Label: k, Unit: bbSeriesUnit(k)}
		if m4 {
			line.M4 = bk.pointsM4(k)
		} else {
			line.Points = bk.points(k)
		}
		lines = append(lines, line)
	}

	// The busiest processes over the window, by their highest CPU reading.
	peak := map[string]float64{}
	for k, row := range procBk.byKey {
		if !strings.HasSuffix(k, ".cpu") {
			continue
		}
		hi := 0.0
		for _, a := range row {
			if a.n > 0 && a.hi > hi {
				hi = a.hi
			}
		}
		peak[strings.TrimSuffix(strings.TrimPrefix(k, "p."), ".cpu")] = hi
	}
	names := make([]string, 0, len(peak))
	for nm := range peak {
		names = append(names, nm)
	}
	sort.Slice(names, func(i, j int) bool {
		if peak[names[i]] != peak[names[j]] {
			return peak[names[i]] > peak[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > bbSeriesMaxProcs {
		names = names[:bbSeriesMaxProcs]
	}
	procs := make([]bbSeriesProc, 0, len(names))
	for _, nm := range names {
		pr := bbSeriesProc{Name: nm, PeakCP: peak[nm]}
		if m4 {
			pr.M4On = true
			pr.M4CPU, pr.M4WS, pr.M4Priv = procBk.pointsM4("p."+nm+".cpu"), procBk.pointsM4("p."+nm+".ws"), procBk.pointsM4("p."+nm+".priv")
		} else {
			pr.CPU, pr.WS, pr.Priv = procBk.points("p."+nm+".cpu"), procBk.points("p."+nm+".ws"), procBk.points("p."+nm+".priv")
		}
		procs = append(procs, pr)
	}

	evs := make([]bbSeriesEventID, 0, len(ids))
	for k, c := range ids {
		evs = append(evs, bbSeriesEventID{Channel: uint8(k[0]), EventID: k[1], Level: uint8(k[2]), Count: c})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].Count != evs[j].Count {
			return evs[i].Count > evs[j].Count
		}
		return evs[i].EventID < evs[j].EventID
	})
	if len(evs) > bbSeriesMaxEventIDs {
		evs = evs[:bbSeriesMaxEventIDs]
	}
	dl := make([]bbSeriesDump, 0, len(dumps))
	for _, d := range dumps {
		dl = append(dl, d)
	}
	sort.Slice(dl, func(i, j int) bool { return dl[i].MTime.After(dl[j].MTime) })
	if len(dl) > bbSeriesMaxDumps {
		dl = dl[:bbSeriesMaxDumps]
	}

	// Gaps: what the recorder wrote down, plus any hole the samples themselves show.
	gaps := []bbSeriesGap{}
	seen := map[time.Time]bool{}
	for _, g := range m.Gaps {
		if g.To.Before(win.Start) || g.From.After(win.End) {
			continue
		}
		seen[g.From.UTC()] = true
		gaps = append(gaps, bbSeriesGap{From: g.From.UTC(), To: g.To.UTC(), Seconds: g.Seconds, Cause: g.Cause})
	}
	for _, g := range blackbox.DetectGaps(times, m.CadenceSeconds(), blackbox.CauseLate) {
		if !seen[g.From.UTC()] {
			gaps = append(gaps, bbSeriesGap{From: g.From.UTC(), To: g.To.UTC(), Seconds: g.Seconds, Cause: g.Cause})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].From.Before(gaps[j].From) })
	marks := []bbSeriesMarker{}
	for _, ev := range m.Events {
		if ev.T.Before(win.Start) || ev.T.After(win.End) {
			continue
		}
		marks = append(marks, bbSeriesMarker{T: ev.T.UTC(), Kind: ev.Kind, Detail: ev.Detail})
	}

	groups := make([]bbSeriesGroupCover, 0, len(bbSeriesGroups))
	for _, g := range bbSeriesGroups {
		groups = append(groups, *cover[g])
	}
	if c := cover[bbDmvGroup]; c != nil {
		groups = append(groups, *c)
	}
	ans := bbSeriesAnswer{
		Start: win.Start, End: win.End, BucketSeconds: int(width / time.Second), IntervalSeconds: m.CadenceSeconds(),
		Samples: len(samples), Lines: lines, Groups: groups, Processes: procs, EventIDs: evs, Dumps: dl,
		Gaps: gaps, Markers: marks, Read: summarize(rep), State: state, Truncated: bk.over, SQL: bbSQLSummary(sqlIn),
	}
	if len(samples) > 0 {
		f, l := samples[0].T.UTC(), samples[len(samples)-1].T.UTC()
		ans.First, ans.Last = &f, &l
	}
	return ans
}
