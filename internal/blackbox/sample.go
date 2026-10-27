package blackbox

import (
	"time"
)

// FormatVersion is the version of the file and status formats written here.
// Readers that see a newer one report it and do not guess (wireframe screen 35).
const FormatVersion = 1

// Sampling rules.
const (
	// LateIntervals: more than this many intervals without a sample is Running
	// late (decision A36). The same count makes a gap in the recording.
	LateIntervals = 3
	// TopProcesses is how many processes each sample lists.
	TopProcesses = 20
	// MaxLogEntries bounds the logs catalogue in one sample.
	MaxLogEntries = 200
)

// Sample is one line of an hourly NDJSON file. A measurement that could not be
// read is left out (never written as zero): readers count a missing value as
// missing, not as 0. Missing names the groups that could not be read this time.
type Sample struct {
	V   int       `json:"v"`
	Run string    `json:"run"` // id of the recorder run; changes at every start
	Seq uint64    `json:"seq"` // 1, 2, 3... within a run
	T   time.Time `json:"t"`   // UTC

	CPU         *float64 `json:"cpu,omitempty"`         // host CPU busy, percent, over the last interval
	MemAvail    *uint64  `json:"memAvail,omitempty"`    // bytes
	MemCommit   *uint64  `json:"memCommit,omitempty"`   // bytes
	DiskQueue   *float64 `json:"diskQueue,omitempty"`   // current disk queue length, all disks
	NetIn       *uint64  `json:"netIn,omitempty"`       // cumulative bytes received, all interfaces
	NetOut      *uint64  `json:"netOut,omitempty"`      // cumulative bytes sent
	DiskFreePct *float64 `json:"diskFreePct,omitempty"` // free space of the recordings volume

	Procs   []ProcRow  `json:"procs,omitempty"`
	Logs    []LogEntry `json:"logs,omitempty"` // names, sizes and times only
	Missing []string   `json:"missing,omitempty"`
}

// ProcRow is one process in a sample. CPUSec is cumulative so a reader can
// recompute the change; CPUPct is the change over the last interval as percent
// of the whole machine.
type ProcRow struct {
	PID     uint32   `json:"pid"`
	Name    string   `json:"name"` // image base name, never a path or command line
	Start   int64    `json:"start,omitempty"`
	CPUSec  *float64 `json:"cpuSec,omitempty"`
	CPUPct  *float64 `json:"cpuPct,omitempty"`
	WS      *uint64  `json:"ws,omitempty"`
	Private *uint64  `json:"priv,omitempty"`
	Handles *uint32  `json:"handles,omitempty"`
	Threads uint32   `json:"threads"`
}

// LogEntry is one catalogued log file: where, how big, when last written.
type LogEntry struct {
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// Collector reads the measurements of one sample. Implementations keep what
// they need between calls (previous CPU times). Collect fills the measurement
// fields and Missing; the recorder adds V, Run, Seq, T, DiskFreePct and Logs.
type Collector interface {
	Collect(now time.Time) (Sample, error)
	Close() error
}

// Gap is a stretch with no sample: from the last sample before it to the first
// one after.
type Gap struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Seconds int       `json:"seconds"`
	// Cause is "late" (the recorder ran but did not sample), "restart" (stopped
	// cleanly and started again), "crash" (the recorder ended without a clean
	// stop: power loss, killed), or "disk-low" (paused for free space).
	Cause string `json:"cause"`
}

// Gap causes.
const (
	CauseLate    = "late"
	CauseRestart = "restart"
	CauseCrash   = "crash"
	CauseDiskLow = "disk-low"
)

// GapBetween says whether the time from prev to next is a gap: more than
// LateIntervals intervals. The cause is passed through.
func GapBetween(prev, next time.Time, intervalSeconds int, cause string) (Gap, bool) {
	d := next.Sub(prev)
	limit := time.Duration(LateIntervals*intervalSeconds) * time.Second
	if d <= limit {
		return Gap{}, false
	}
	return Gap{From: prev.UTC(), To: next.UTC(), Seconds: int(d.Seconds()), Cause: cause}, true
}

// DetectGaps finds the gaps in sample times that are in order. A time that is
// not later than the one before is skipped: it does not open a gap.
func DetectGaps(times []time.Time, intervalSeconds int, cause string) []Gap {
	var out []Gap
	var prev time.Time
	for _, t := range times {
		if prev.IsZero() {
			prev = t
			continue
		}
		if !t.After(prev) {
			continue
		}
		if g, ok := GapBetween(prev, t, intervalSeconds, cause); ok {
			out = append(out, g)
		}
		prev = t
	}
	return out
}

// IsLate says whether the newest sample is too old for a recorder that is
// running (more than LateIntervals intervals).
func IsLate(last, now time.Time, intervalSeconds int) bool {
	if last.IsZero() {
		return false
	}
	return now.Sub(last) > time.Duration(LateIntervals*intervalSeconds)*time.Second
}
