package blackbox

import (
	"errors"
	"time"

	"api-audit-explorer/internal/sampler"
)

// Extras are the counters the in-app sampler does not read: disk queue and
// network totals. Each may fail on its own; a failure leaves that measurement
// out of the sample and names it in Missing.
type Extras interface {
	DiskQueue() (float64, error)
	// DiskLatencyMs: average seconds per transfer of all disks, in ms, from the same collect as DiskQueue; call it right after DiskQueue.
	DiskLatencyMs() (float64, error)
	Network() (in, out uint64, err error)
	Close() error
}

// NoExtras reads nothing; every extra counter is reported missing.
type NoExtras struct{}

func (NoExtras) DiskQueue() (float64, error)      { return 0, errors.New("not available") }
func (NoExtras) DiskLatencyMs() (float64, error)  { return 0, errors.New("not available") }
func (NoExtras) Network() (uint64, uint64, error) { return 0, 0, errors.New("not available") }
func (NoExtras) Close() error                     { return nil }

// HostCollector reads the host through the sampler's Source (CPU times, memory,
// processes) plus Extras. It reuses internal/sampler as it is.
type HostCollector struct {
	src       sampler.Source
	extras    Extras
	processes bool
	selected  func() []ResolvedTarget

	cores    int
	prevHost sampler.HostTimes
	havePrev bool
	prevAt   time.Time
	// prevProcAt is the time of the last SUCCESSFUL process snapshot. It is
	// tracked separately from prevAt (the host read) so a failed process
	// enumeration never advances the process-rate denominator: the CPU
	// numerator and its wall-time baseline always span the same interval
	// (W6-05). It is zero until the first successful process read.
	prevProcAt time.Time
	prevProc   map[procID]float64
	procs      []sampler.ProcSample
	// infoScratch is the buffer SnapshotProcesses reads into.
	infoScratch []sampler.ProcSample
	// Reused every tick by topProcesses.
	cpuTop, memTop, picked []scored
}

type procID struct {
	pid   uint32
	start int64
}

// NewHostCollector wraps src and extras. processes says whether the process
// list is recorded. The collector owns src and extras and closes them.
func NewHostCollector(src sampler.Source, extras Extras, processes bool) *HostCollector {
	if extras == nil {
		extras = NoExtras{}
	}
	cores := src.Cores()
	if cores < 1 {
		cores = 1
	}
	return &HostCollector{src: src, extras: extras, processes: processes, cores: cores, prevProc: map[procID]float64{}}
}

// SnapshotProcesses lists the processes as the resolver wants them (PID, parent,
// name, creation identity), read through the same Source the host collector uses.
// On Windows the Source serves a read taken a few milliseconds earlier to this
// second caller, so a tick lists the machine once, not twice (perf finding
// 2026-11-18). It never touches h.procs: the collector's own list is separate.
func (h *HostCollector) SnapshotProcesses() ([]ProcInfo, error) {
	var err error
	h.infoScratch, err = h.src.Processes(h.infoScratch[:0])
	if err != nil {
		return nil, err
	}
	out := make([]ProcInfo, 0, len(h.infoScratch))
	for i := range h.infoScratch {
		p := &h.infoScratch[i]
		pi := ProcInfo{PID: p.PID, ParentPID: p.Parent, Name: p.Image}
		if p.Have&sampler.HaveTimes != 0 && !p.Start.IsZero() {
			pi.Start = p.Start.Unix()
			pi.StartTicks = p.Start.UnixNano()/100 + filetimeUnixEpochDiff
		}
		out = append(out, pi)
	}
	return out, nil
}

// SetSelectedTargets wires the resolver's selected targets: each one is always
// listed in the process rows, even when idle. A nil func lists no extra targets.
func (h *HostCollector) SetSelectedTargets(f func() []ResolvedTarget) { h.selected = f }

// Prime reads the host once so the first Collect has a baseline to take changes
// from. It records nothing.
func (h *HostCollector) Prime(now time.Time) error {
	hs, err := h.src.Host()
	if err != nil {
		return err
	}
	h.prevHost, h.havePrev, h.prevAt = hs.Times, true, now
	if h.processes {
		h.procs, err = h.src.Processes(h.procs[:0])
		if err != nil {
			return err
		}
		h.remember()
		h.prevProcAt = now
	}
	return nil
}

func (h *HostCollector) remember() {
	clear(h.prevProc)
	for i := range h.procs {
		p := &h.procs[i]
		if p.Have&sampler.HaveTimes != 0 {
			h.prevProc[idOf(p)] = p.CPUSeconds
		}
	}
}

func idOf(p *sampler.ProcSample) procID {
	id := procID{pid: p.PID}
	if p.Have&sampler.HaveTimes != 0 && !p.Start.IsZero() {
		id.start = p.Start.Unix()
	}
	return id
}

// Collect reads everything once. It fails only when the host itself cannot be
// read; a counter that fails is left out and named in Missing.
func (h *HostCollector) Collect(now time.Time) (Sample, error) {
	var s Sample
	hs, err := h.src.Host()
	if err != nil {
		return s, err
	}
	if h.havePrev {
		dTotal := (hs.Times.Kernel - h.prevHost.Kernel) + (hs.Times.User - h.prevHost.User)
		dIdle := hs.Times.Idle - h.prevHost.Idle
		if hs.Times.Kernel >= h.prevHost.Kernel && hs.Times.User >= h.prevHost.User && hs.Times.Idle >= h.prevHost.Idle && dTotal > 0 && dIdle <= dTotal {
			pct := float64(dTotal-dIdle) / float64(dTotal) * 100
			s.CPU = &pct
		} else {
			s.Missing = append(s.Missing, "cpu")
		}
	}
	avail, commit := hs.AvailableBytes, hs.CommittedBytes
	s.MemAvail, s.MemCommit = &avail, &commit

	if q, err := h.extras.DiskQueue(); err == nil {
		s.DiskQueue = &q
	} else {
		s.Missing = append(s.Missing, "diskQueue")
	}
	if ms, err := h.extras.DiskLatencyMs(); err == nil {
		s.DiskMs = &ms
	} else {
		s.Missing = append(s.Missing, "diskMs")
	}
	if in, out, err := h.extras.Network(); err == nil {
		s.NetIn, s.NetOut = &in, &out
	} else {
		s.Missing = append(s.Missing, "network")
	}

	if h.processes {
		var perr error
		h.procs, perr = h.src.Processes(h.procs[:0])
		if perr != nil {
			s.Missing = append(s.Missing, "processes")
			// Leave prevProc and prevProcAt untouched: the CPU baselines keep
			// their own snapshot time, so the next successful read divides a
			// numerator and denominator that span the same interval (W6-05).
			// Host success below must not advance the process-rate clock.
		} else {
			var wall float64
			if !h.prevProcAt.IsZero() {
				wall = now.Sub(h.prevProcAt).Seconds()
			}
			s.Procs = h.topProcesses(wall)
			h.remember()
			h.prevProcAt = now
		}
	}
	h.prevHost, h.havePrev, h.prevAt = hs.Times, true, now
	return s, nil
}

// scored is one process with the numbers it is ranked by.
type scored struct {
	i     int
	delta float64
	have  bool
	ws    uint64
}

// cpuBefore ranks by CPU change, then the lower PID first.
func (h *HostCollector) cpuBefore(a, b scored) bool {
	if a.delta != b.delta {
		return a.delta > b.delta
	}
	return h.procs[a.i].PID < h.procs[b.i].PID
}

// memBefore ranks by working set; an unreadable one ranks last.
func (h *HostCollector) memBefore(a, b scored) bool {
	if a.ws != b.ws {
		return a.ws > b.ws
	}
	return h.procs[a.i].PID < h.procs[b.i].PID
}

// insertTop keeps the best k items of a stream in order. dst never grows past k.
func insertTop(dst []scored, k int, x scored, before func(a, b scored) bool) []scored {
	n := len(dst)
	if n == k {
		if !before(x, dst[n-1]) {
			return dst
		}
		n--
	} else {
		dst = append(dst, x)
	}
	j := n
	for j > 0 && before(x, dst[j-1]) {
		dst[j] = dst[j-1]
		j--
	}
	dst[j] = x
	return dst
}

// topProcesses lists the busiest processes: the top TopCPU by CPU change over
// the last interval plus the top TopMem by working set, merged without
// duplicates, plus every selected target even when idle. Idle (PID 0) is never
// listed. All processes are read and ranked; only the listing is smaller. On
// the first sample there is no change to take, so the cumulative CPU time
// orders them and no percent is given.
func (h *HostCollector) topProcesses(wall float64) []ProcRow {
	// Only TopCPU and TopMem rows are kept, so the whole list is never sorted: a
	// bounded insertion keeps the best k seen so far. Both orders are total (the
	// PID breaks every tie), so the result equals a full sort. The buffers live on
	// the collector and are reused every tick.
	cpu := h.cpuTop[:0]
	mem := h.memTop[:0]
	havePrevProc := h.havePrev && len(h.prevProc) > 0
	for i := range h.procs {
		p := &h.procs[i]
		if p.PID == 0 {
			continue
		}
		sc := scored{i: i}
		if p.Have&sampler.HaveTimes != 0 {
			if prev, ok := h.prevProc[idOf(p)]; ok && p.CPUSeconds >= prev {
				sc.delta, sc.have = p.CPUSeconds-prev, true
			} else if !havePrevProc {
				sc.delta = p.CPUSeconds
			}
		}
		sc.ws = h.workingSet(i)
		cpu = insertTop(cpu, TopCPU, sc, h.cpuBefore)
		mem = insertTop(mem, TopMem, sc, h.memBefore)
	}
	h.cpuTop, h.memTop = cpu, mem
	picked := h.picked[:0]
	add := func(sc scored) {
		id := idOf(&h.procs[sc.i])
		for _, q := range picked {
			if idOf(&h.procs[q.i]) == id {
				return
			}
		}
		picked = append(picked, sc)
	}
	for _, sc := range cpu {
		add(sc)
	}
	for _, sc := range mem {
		add(sc)
	}
	// Selected targets are always listed, even when idle: a measured zero is
	// a real reading of a running process, not a missing one.
	if h.selected != nil {
		for _, t := range h.selected() {
			if t.PID == 0 {
				continue
			}
			want := procID{pid: t.PID, start: t.Start}
			found := false
			for _, q := range picked {
				if idOf(&h.procs[q.i]) == want {
					found = true
					break
				}
			}
			if found {
				continue
			}
			for i := range h.procs {
				p := &h.procs[i]
				if p.PID == 0 || idOf(p) != want {
					continue
				}
				sc := scored{i: i}
				if p.Have&sampler.HaveTimes != 0 {
					if prev, ok := h.prevProc[want]; ok && p.CPUSeconds >= prev {
						sc.delta, sc.have = p.CPUSeconds-prev, true
					} else if !havePrevProc {
						sc.delta = p.CPUSeconds
					}
				}
				add(sc)
				break
			}
		}
	}
	h.picked = picked
	rows := make([]ProcRow, 0, len(picked))
	for _, sc := range picked {
		p := &h.procs[sc.i]
		r := ProcRow{PID: p.PID, Name: p.Image, Threads: p.Threads}
		if p.Have&sampler.HaveTimes != 0 {
			cs := p.CPUSeconds
			r.CPUSec = &cs
			if !p.Start.IsZero() {
				r.Start = p.Start.Unix()
			}
			if sc.have && wall > 0 {
				pct := sc.delta / wall / float64(h.cores) * 100
				r.CPUPct = &pct
			}
		}
		if p.Have&sampler.HaveMemory != 0 {
			ws, pb := p.WorkingSet, p.PrivateBytes
			r.WS, r.Private = &ws, &pb
		}
		if p.Have&sampler.HaveHandles != 0 {
			hc := p.Handles
			r.Handles = &hc
		}
		rows = append(rows, r)
	}
	return rows
}

// workingSet is the working set used to rank the memory top; an unreadable
// working set ranks as 0 (ranking only, the row keeps nil).
func (h *HostCollector) workingSet(i int) uint64 {
	p := &h.procs[i]
	if p.Have&sampler.HaveMemory != 0 {
		return p.WorkingSet
	}
	return 0
}

// Close closes the Source and the extras.
func (h *HostCollector) Close() error {
	err1 := h.src.Close()
	err2 := h.extras.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
