package codemode

import (
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/bindings"
	"api-audit-explorer/internal/codemode/wire"
)

// The limits of a run. The ones a script is told about come from the bindings package, so the text a model reads and the rules the
// broker enforces are the same constants.
const (
	MaxInFlight    = bindings.MaxInFlight
	MaxCalls       = bindings.MaxCalls
	MaxResultBytes = bindings.MaxResultBytes
	MaxLogBytes    = bindings.MaxLogBytes
	RunCap         = bindings.RunCapSeconds * time.Second

	// MaxSource is the largest script (spec 04: 256 KiB).
	MaxSource = casestore.MaxRunSource
	// MaxStrippedJS bounds what esbuild makes of a script (an enum or a long template can grow it).
	MaxStrippedJS = 1 << 20
	// MaxConcurrentRuns is the number of runs at the same time (spec D28); the next one is refused with busy.
	MaxConcurrentRuns = 2
	// MaxCallTime is the longest one host call may run (the operation deadline of the tools).
	MaxCallTime = 5 * time.Minute

	DefaultTimeout = 30 * time.Second
	MinTimeout     = time.Second
	MaxTimeout     = 120 * time.Second

	// HelloTimeout is how long a worker has to say hello after it started.
	HelloTimeout = 10 * time.Second
	// KillWait is how long the broker waits for a killed worker to be gone before it stops waiting (the process is killed anyway).
	KillWait = 5 * time.Second
	// AuditWait bounds one audit write of a call; an import on the case can hold the folder for as long as it runs (the store waits
	// by itself up to casestore.InflightWait).
	AuditWait = 2 * time.Minute

	maxToolNameBytes = 64
)

// workerLimits is what the broker tells the worker (spec 3.3).
func workerLimits() wire.Limits {
	return wire.Limits{MemPages: 1024, HeapBytes: 32 << 20, StackBytes: 512 << 10, Ticks: 500000, CallBytes: MaxResultBytes, LogBytes: MaxLogBytes, MaxCalls: MaxCalls, InFlight: MaxInFlight}
}
