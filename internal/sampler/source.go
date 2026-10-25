package sampler

import (
	"errors"
	"time"
)

// ErrNotSupportedOS is returned by OSSource on a system the sampler cannot read.
var ErrNotSupportedOS = errors.New("the live sampler runs only on Windows")

// HostTimes are the cumulative system times from GetSystemTimes, in 100 ns
// units. Kernel includes Idle, as the API reports it.
type HostTimes struct {
	Idle, Kernel, User uint64
}

// HostSample is one read of the host.
type HostSample struct {
	Times          HostTimes
	AvailableBytes uint64
	CommittedBytes uint64
}

// Which fields of a ProcSample the source could read.
const (
	HaveTimes   uint8 = 1 << iota // Start and CPUSeconds
	HaveMemory                    // WorkingSet and PrivateBytes
	HaveHandles                   // Handles
)

// ProcSample is one process at one tick. PID, Image and Threads come from the
// process list and are always set; the other fields are set only when Have says
// the source could read them. A process that could not be opened has Have 0
// ("access denied": name and threads only).
type ProcSample struct {
	PID          uint32
	Image        string // image base name, never a path or a command line
	Threads      uint32
	Have         uint8
	Start        time.Time // creation time
	CPUSeconds   float64   // cumulative kernel plus user time
	WorkingSet   uint64
	PrivateBytes uint64
	Handles      uint32
}

// Source is where the sampler reads the operating system. The Windows source is
// in source_windows.go; tests use a fake, so the sampling, batching, cap and
// carry logic runs on any system.
type Source interface {
	// Cores is the number of logical cores of the host.
	Cores() int
	// Host reads the host times and memory.
	Host() (HostSample, error)
	// Processes appends every process to dst (which may be nil) and returns it.
	Processes(dst []ProcSample) ([]ProcSample, error)
	// Close releases what the source holds.
	Close() error
}
