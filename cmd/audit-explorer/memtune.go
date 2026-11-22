package main

import (
	"os"
	"runtime/debug"
)

const (
	// memorySoftLimit is the floor of the soft memory limit and what the app used
	// before the limit followed the machine (256 MiB). It is never lowered.
	memorySoftLimit = 256 << 20
	// memorySoftLimitCap is the most the soft limit rises to on a large machine.
	memorySoftLimitCap = 512 << 20
)

// tuneMemory returns the soft memory limit for a machine with totalRAM bytes: a
// sixteenth of the RAM, never under 256 MiB and never over 512 MiB (an unknown RAM,
// 0, gives the floor). So 1, 2 and 4 GiB give 256 MiB and 8 GiB and up give 512 MiB.
//
// Spec 07 section 3.2 gave a formula (min(RAM/4, 512 MiB)) and a test table (1, 2,
// 4, 8, 32 GiB giving 256, 256, 256, 512, 512 MiB) that disagree; the table is the
// one that matches the 2 GB machine the acceptance names, so it is the one built.
// The collector is never switched off.
//
// The GC percent is deliberately left alone (the spec proposed 50). Measured on the
// counters import (docs/desktop/bench-bl423.md): GOGC=50 cost 8 to 12 percent more
// CPU and 10 to 15 percent more wall time, and saved no memory, because the Go heap
// is only 10 to 20 MiB; the database memory sits outside it.
func tuneMemory(totalRAM uint64) int64 {
	return int64(min(max(totalRAM/16, memorySoftLimit), memorySoftLimitCap))
}

// applyMemoryTuning sets the soft limit unless the person set GOMEMLIMIT, which
// the runtime has already applied and which wins. GOGC is never touched, so the
// person's value (or the default) stays. run and runMCP call it once at start.
func applyMemoryTuning() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	debug.SetMemoryLimit(tuneMemory(totalRAM()))
}
