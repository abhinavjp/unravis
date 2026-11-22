package main

import "runtime/debug"

const (
	// memorySoftLimit is the floor of the soft memory limit and what the app used
	// before the limit followed the machine (256 MiB). It is never lowered.
	memorySoftLimit = 256 << 20
	// memorySoftLimitCap is the most the soft limit rises to on a large machine.
	memorySoftLimitCap = 512 << 20
)

// tuneMemory returns the soft memory limit for a machine with totalRAM bytes: a
// quarter of the RAM, never under 256 MiB and never over 512 MiB (an unknown RAM,
// 0, gives the floor). The collector is never switched off.
//
// The GC percent is deliberately left alone (BL-423 spec 3.2 proposed 50). Measured
// on the counters import (docs/desktop/bench-bl423.md): GOGC=50 cost 8 to 12 percent
// more CPU and 10 to 15 percent more wall time, and saved no memory, because the
// Go heap is only 10 to 20 MiB; the database memory sits outside it. A person who
// sets GOGC in the environment keeps it, as the runtime reads that itself.
func tuneMemory(totalRAM uint64) int64 {
	limit := int64(memorySoftLimit)
	if q := int64(min(totalRAM/4, memorySoftLimitCap)); q > limit {
		limit = q
	}
	return limit
}

// applyMemoryTuning sets the soft limit. run and runMCP call it once at start.
func applyMemoryTuning() { debug.SetMemoryLimit(tuneMemory(totalRAM())) }
