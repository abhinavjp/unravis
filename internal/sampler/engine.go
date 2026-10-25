package sampler

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/importer/procsnap"
	"api-audit-explorer/internal/importer/winlive"
)

// TopN is how many processes are listed at each tick by CPU change and again by
// private bytes. The two lists are joined, and every server image is added.
const TopN = 15

// errLimit is returned by tick when the next tick would pass the event cap.
var errLimit = errors.New("event cap reached")

// ident names one process: pid and start time (unix seconds; 0 when the start
// could not be read).
type ident struct {
	pid   uint32
	start int64
}

type cpuPoint struct {
	sec float64
	t   time.Time
}

type trackedInfo struct {
	label    string
	lastSeen time.Time
}

type carryRow struct {
	id    ident
	label string
	pt    cpuPoint
}

// engine is the sampling state: it turns each read of the Source into batch
// rows and keeps what the next tick and the next batch need. It does no I/O of
// its own and takes the sample time as an argument, so it is tested without a
// clock. One goroutine uses an engine.
type engine struct {
	src       Source
	pseudo    func(kind, value string) string
	interval  int
	cores     int
	maxEvents int
	onPartial func(label string, pid uint32)

	prevHost  HostTimes
	havePrev  bool
	prev, cur map[ident]cpuPoint // cumulative CPU of every process with times
	labels    map[ident]string
	tracked   map[ident]trackedInfo // processes that have had rows
	announced map[ident]bool        // process-start marks written
	initial   map[ident]bool        // present when the run began
	partial   map[ident]bool        // access-denied notes written

	// The batch being built.
	data       bytes.Buffer
	carry      []carryRow
	batchSeen  map[ident]bool
	batchFirst time.Time
	batchRows  int

	// Counters.
	events  int
	samples int

	// Scratch, reused every tick.
	procs     []ProcSample
	selected  []int
	chosen    []bool
	tickBuf   bytes.Buffer
	tickCarry []carryRow
	tickSeen  []ident
	line      []byte
}

func newEngine(src Source, pseudo func(kind, value string) string, interval, maxEvents int, onPartial func(string, uint32)) *engine {
	cores := src.Cores()
	if cores < 1 {
		cores = 1
	}
	if cores > 4096 {
		cores = 4096
	}
	return &engine{
		src: src, pseudo: pseudo, interval: interval, cores: cores, maxEvents: maxEvents, onPartial: onPartial,
		prev: map[ident]cpuPoint{}, cur: map[ident]cpuPoint{}, labels: map[ident]string{},
		tracked: map[ident]trackedInfo{}, announced: map[ident]bool{}, initial: map[ident]bool{},
		partial: map[ident]bool{}, batchSeen: map[ident]bool{},
	}
}

func identOf(p *ProcSample) ident {
	id := ident{pid: p.PID}
	if p.Have&HaveTimes != 0 && !p.Start.IsZero() {
		id.start = p.Start.Unix()
	}
	return id
}

// prime reads the host and the processes once, so the first tick has something
// to take a change from. It writes nothing.
func (e *engine) prime(now time.Time) error {
	h, err := e.src.Host()
	if err != nil {
		return err
	}
	e.procs, err = e.src.Processes(e.procs[:0])
	if err != nil {
		return err
	}
	e.prevHost, e.havePrev = h.Times, true
	t := now.UTC().Truncate(time.Second)
	for i := range e.procs {
		p := &e.procs[i]
		if p.PID == 0 {
			continue
		}
		id := identOf(p)
		e.initial[id] = true
		if p.Have&HaveTimes != 0 {
			e.prev[id] = cpuPoint{p.CPUSeconds, t}
		}
	}
	return nil
}

// tick reads the host and the processes at now and adds the rows of this tick
// to the batch. It returns errLimit, with nothing added, when the tick would
// take the events past the cap. Any other error is a read that failed; nothing
// is added then either.
func (e *engine) tick(now time.Time) error {
	h, err := e.src.Host()
	if err != nil {
		return err
	}
	e.procs, err = e.src.Processes(e.procs[:0])
	if err != nil {
		return err
	}
	t := now.UTC().Truncate(time.Second)
	ts := t.Format(winlive.TimeFormat)
	e.tickBuf.Reset()
	e.tickCarry = e.tickCarry[:0]
	e.tickSeen = e.tickSeen[:0]
	rows := 0

	// Host.
	if e.havePrev {
		dTotal := (h.Times.Kernel - e.prevHost.Kernel) + (h.Times.User - e.prevHost.User)
		dIdle := h.Times.Idle - e.prevHost.Idle
		if h.Times.Kernel >= e.prevHost.Kernel && h.Times.User >= e.prevHost.User && h.Times.Idle >= e.prevHost.Idle && dTotal > 0 && dIdle <= dTotal {
			pct := float64(dTotal-dIdle) / float64(dTotal) * 100
			e.row(ts, winlive.ScopeHost, "", 0, time.Time{}, winlive.MetricHostCPU, strconv.FormatFloat(pct, 'f', 2, 64))
			rows++
		}
	}
	e.row(ts, winlive.ScopeHost, "", 0, time.Time{}, winlive.MetricMemAvailable, strconv.FormatUint(h.AvailableBytes, 10))
	e.row(ts, winlive.ScopeHost, "", 0, time.Time{}, winlive.MetricMemCommitted, strconv.FormatUint(h.CommittedBytes, 10))
	rows += 2

	// Choose the processes: the busiest by CPU change and by private bytes, and
	// every server image.
	n := len(e.procs)
	if cap(e.chosen) < n {
		e.chosen = make([]bool, n)
	}
	e.chosen = e.chosen[:n]
	clear(e.chosen)
	clear(e.cur)
	var topCPU, topMem topK
	for i := range e.procs {
		p := &e.procs[i]
		if p.PID == 0 {
			continue
		}
		id := identOf(p)
		if p.Have&HaveTimes != 0 {
			e.cur[id] = cpuPoint{p.CPUSeconds, t}
			if pp, ok := e.prev[id]; ok {
				if d := p.CPUSeconds - pp.sec; d > 0 {
					topCPU.add(i, d)
				}
			}
		} else {
			e.cur[id] = cpuPoint{sec: -1} // present, no CPU time
		}
		if p.Have&HaveMemory != 0 && p.PrivateBytes > 0 {
			topMem.add(i, float64(p.PrivateBytes))
		}
		if procsnap.IsServerImage(imageKey(p.Image)) {
			e.chosen[i] = true
		}
	}
	for _, i := range topCPU.idx[:topCPU.n] {
		e.chosen[i] = true
	}
	for _, i := range topMem.idx[:topMem.n] {
		e.chosen[i] = true
	}
	e.selected = e.selected[:0]
	for i := range e.procs {
		if e.chosen[i] {
			e.selected = append(e.selected, i)
		}
	}
	slices.SortFunc(e.selected, func(a, b int) int { return int(e.procs[a].PID) - int(e.procs[b].PID) })

	// Rows of the chosen processes.
	for _, i := range e.selected {
		p := &e.procs[i]
		id := identOf(p)
		label := e.labelOf(id, p)
		if !winlive.ValidProcess(label) {
			continue // the batch parser would refuse the file; the process is left out
		}
		start := time.Time{}
		if id.start != 0 {
			start = time.Unix(id.start, 0).UTC()
		}
		if p.Have&HaveTimes != 0 {
			if pp, ok := e.prev[id]; ok && id.start != 0 && !e.batchSeen[id] {
				e.tickCarry = append(e.tickCarry, carryRow{id: id, label: label, pt: pp})
			}
			e.tickSeen = append(e.tickSeen, id)
			if !e.initial[id] && !e.announced[id] && !start.IsZero() {
				e.announced[id] = true
				e.row(ts, winlive.ScopeMark, label, p.PID, start, winlive.MetricStart, "0")
				rows++
			}
			e.row(ts, winlive.ScopeProc, label, p.PID, start, winlive.MetricCPUSeconds, strconv.FormatFloat(p.CPUSeconds, 'f', 3, 64))
			rows++
		}
		if p.Have&HaveMemory != 0 {
			e.row(ts, winlive.ScopeProc, label, p.PID, start, winlive.MetricWorkingSet, strconv.FormatUint(p.WorkingSet, 10))
			e.row(ts, winlive.ScopeProc, label, p.PID, start, winlive.MetricPrivateBytes, strconv.FormatUint(p.PrivateBytes, 10))
			rows += 2
		}
		if p.Have&HaveHandles != 0 {
			e.row(ts, winlive.ScopeProc, label, p.PID, start, winlive.MetricHandles, strconv.FormatUint(uint64(p.Handles), 10))
			rows++
		}
		e.row(ts, winlive.ScopeProc, label, p.PID, start, winlive.MetricThreads, strconv.FormatUint(uint64(p.Threads), 10))
		rows++
		if p.Have == 0 && !e.partial[id] {
			e.partial[id] = true
			if e.onPartial != nil {
				e.onPartial(label, p.PID)
			}
		}
	}

	// A process that had rows and is no longer listed is gone; it was last seen
	// at most one interval ago.
	var gone []ident
	for id := range e.tracked {
		if _, ok := e.cur[id]; !ok {
			gone = append(gone, id)
		}
	}
	slices.SortFunc(gone, func(a, b ident) int { return int(a.pid) - int(b.pid) })
	for _, id := range gone {
		info := e.tracked[id]
		if id.start != 0 {
			span := t.Sub(info.lastSeen).Seconds()
			if span < 0 {
				span = 0
			}
			e.row(ts, winlive.ScopeMark, info.label, id.pid, time.Unix(id.start, 0).UTC(), winlive.MetricGone, strconv.FormatFloat(span, 'f', 0, 64))
			rows++
		}
	}

	if e.events+rows > e.maxEvents {
		return errLimit
	}

	// Commit.
	for _, id := range gone {
		delete(e.tracked, id)
		delete(e.announced, id)
		delete(e.partial, id)
	}
	for _, i := range e.selected {
		p := &e.procs[i]
		id := identOf(p)
		if label, ok := e.labels[id]; ok && winlive.ValidProcess(label) {
			e.tracked[id] = trackedInfo{label: label, lastSeen: t}
		}
	}
	for id := range e.tracked { // the last time every tracked process was listed
		if _, ok := e.cur[id]; ok {
			info := e.tracked[id]
			info.lastSeen = t
			e.tracked[id] = info
		}
	}
	for id := range e.labels { // labels only for processes still listed
		if _, ok := e.cur[id]; !ok {
			delete(e.labels, id)
		}
	}
	for id := range e.initial {
		if _, ok := e.cur[id]; !ok {
			delete(e.initial, id)
		}
	}
	if e.batchRows == 0 {
		e.batchFirst = t
	}
	e.carry = append(e.carry, e.tickCarry...)
	for _, id := range e.tickSeen {
		e.batchSeen[id] = true
	}
	e.data.Write(e.tickBuf.Bytes())
	e.batchRows += rows
	e.events += rows
	e.samples++
	e.prevHost, e.havePrev = h.Times, true
	e.prev, e.cur = e.cur, e.prev
	// A process listed without CPU times is not a CPU baseline.
	for id, pt := range e.prev {
		if pt.sec < 0 {
			delete(e.prev, id)
		}
	}
	return nil
}

// labelOf is the stored name of a process, computed once per process.
func (e *engine) labelOf(id ident, p *ProcSample) string {
	if l, ok := e.labels[id]; ok {
		return l
	}
	l := procsnap.ProcessLabel(p.Image, e.pseudo)
	e.labels[id] = l
	return l
}

// imageKey is an image name as the server image list spells it.
func imageKey(image string) string {
	if n := len(image); n >= 4 && strings.EqualFold(image[n-4:], ".exe") {
		image = image[:n-4]
	}
	return strings.ToLower(image)
}

// row appends one row of the tick being built.
func (e *engine) row(ts, scope, process string, pid uint32, start time.Time, metric, value string) {
	b := e.line[:0]
	b = append(b, ts...)
	b = append(b, ',')
	b = append(b, scope...)
	b = append(b, ',')
	b = append(b, process...)
	b = append(b, ',')
	if pid != 0 {
		b = strconv.AppendUint(b, uint64(pid), 10)
	}
	b = append(b, ',')
	if !start.IsZero() {
		b = start.UTC().AppendFormat(b, winlive.TimeFormat)
	}
	b = append(b, ',')
	b = append(b, metric...)
	b = append(b, ',')
	b = append(b, value...)
	b = append(b, '\n')
	e.tickBuf.Write(b)
	e.line = b
}

// cut ends the batch: it returns the file name and the body, and starts the next
// batch empty. ok is false when the batch holds no rows.
func (e *engine) cut() (name string, body []byte, ok bool) {
	defer func() {
		e.data.Reset()
		e.carry = e.carry[:0]
		clear(e.batchSeen)
		e.batchRows = 0
		e.batchFirst = time.Time{}
	}()
	if e.batchRows == 0 {
		return "", nil, false
	}
	slices.SortFunc(e.carry, func(a, b carryRow) int { return int(a.id.pid) - int(b.id.pid) })
	var out bytes.Buffer
	out.Grow(e.data.Len() + 128*len(e.carry) + 128)
	out.WriteString(winlive.Header(e.interval, e.cores))
	out.WriteByte('\n')
	out.WriteString(winlive.ColumnLine())
	out.WriteByte('\n')
	var line []byte
	for _, c := range e.carry {
		line = c.pt.t.UTC().AppendFormat(line[:0], winlive.TimeFormat)
		line = append(line, ',')
		line = append(line, winlive.ScopeCarry...)
		line = append(line, ',')
		line = append(line, c.label...)
		line = append(line, ',')
		line = strconv.AppendUint(line, uint64(c.id.pid), 10)
		line = append(line, ',')
		line = time.Unix(c.id.start, 0).UTC().AppendFormat(line, winlive.TimeFormat)
		line = append(line, ',')
		line = append(line, winlive.MetricCPUSeconds...)
		line = append(line, ',')
		line = strconv.AppendFloat(line, c.pt.sec, 'f', 3, 64)
		line = append(line, '\n')
		out.Write(line)
	}
	out.Write(e.data.Bytes())
	return "win-live-" + e.batchFirst.UTC().Format("2006-01-02T15-04-05Z") + ".csv", out.Bytes(), true
}

// topK keeps the TopN largest scores seen, without allocating.
type topK struct {
	idx   [TopN]int
	score [TopN]float64
	n     int
}

func (k *topK) add(i int, s float64) {
	if k.n == TopN && s <= k.score[k.n-1] {
		return
	}
	pos := k.n
	if pos == TopN {
		pos = TopN - 1
	} else {
		k.n++
	}
	for pos > 0 && k.score[pos-1] < s {
		k.idx[pos], k.score[pos] = k.idx[pos-1], k.score[pos-1]
		pos--
	}
	k.idx[pos], k.score[pos] = i, s
}
