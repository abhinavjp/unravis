package blackbox

import (
	"errors"
	"sort"
	"time"

	"api-audit-explorer/internal/sampler"
)

// Extras are the counters the in-app sampler does not read: disk queue and
// network totals. Each may fail on its own; a failure leaves that measurement
// out of the sample and names it in Missing.
type Extras interface {
	DiskQueue() (float64, error)
	Network() (in, out uint64, err error)
	Close() error
}

// NoExtras reads nothing; every extra counter is reported missing.
type NoExtras struct{}

func (NoExtras) DiskQueue() (float64, error)      { return 0, errors.New("not available") }
func (NoExtras) Network() (uint64, uint64, error) { return 0, 0, errors.New("not available") }
func (NoExtras) Close() error                     { return nil }

// HostCollector reads the host through the sampler's Source (CPU times, memory,
// processes) plus Extras. It reuses internal/sampler as it is.
type HostCollector struct {
	src       sampler.Source
	extras    Extras
	processes bool

	cores    int
	prevHost sampler.HostTimes
	havePrev bool
	prevAt   time.Time
	prevProc map[procID]float64
	procs    []sampler.ProcSample
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
		} else {
			wall := now.Sub(h.prevAt).Seconds()
			s.Procs = h.topProcesses(wall)
			h.remember()
		}
	}
	h.prevHost, h.havePrev, h.prevAt = hs.Times, true, now
	return s, nil
}

// topProcesses lists the busiest TopProcesses processes by CPU change over the
// last interval. On the first sample there is no change to take, so the
// cumulative CPU time orders them and no percent is given.
func (h *HostCollector) topProcesses(wall float64) []ProcRow {
	type scored struct {
		i     int
		delta float64
		have  bool
	}
	var list []scored
	for i := range h.procs {
		p := &h.procs[i]
		if p.PID == 0 {
			continue
		}
		sc := scored{i: i}
		if p.Have&sampler.HaveTimes != 0 {
			if prev, ok := h.prevProc[idOf(p)]; ok && p.CPUSeconds >= prev {
				sc.delta, sc.have = p.CPUSeconds-prev, true
			} else if !h.havePrev || len(h.prevProc) == 0 {
				sc.delta = p.CPUSeconds
			}
		}
		list = append(list, sc)
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].delta != list[b].delta {
			return list[a].delta > list[b].delta
		}
		return h.procs[list[a].i].PID < h.procs[list[b].i].PID
	})
	if len(list) > TopProcesses {
		list = list[:TopProcesses]
	}
	rows := make([]ProcRow, 0, len(list))
	for _, sc := range list {
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

// Close closes the Source and the extras.
func (h *HostCollector) Close() error {
	err1 := h.src.Close()
	err2 := h.extras.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
