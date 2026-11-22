package main

import (
	"runtime/debug"
	"testing"
)

func TestTuneMemoryTable(t *testing.T) {
	const gib = 1 << 30
	const mib = 1 << 20
	for _, c := range []struct {
		ram  uint64
		want int64
	}{
		{0, 256 * mib},       // unknown RAM: the floor
		{1 * gib, 256 * mib}, // the floor
		{2 * gib, 256 * mib},
		{4 * gib, 256 * mib},
		{8 * gib, 512 * mib}, // a sixteenth of 8 GiB is exactly the cap
		{12 * gib, 512 * mib},
		{32 * gib, 512 * mib},
	} {
		if got := tuneMemory(c.ram); got != c.want {
			t.Errorf("RAM %d GiB: limit %d MiB, want %d MiB", c.ram/gib, got/mib, c.want/mib)
		}
	}
	if got := tuneMemory(1); got < memorySoftLimit {
		t.Errorf("the limit fell under today's 256 MiB: %d", got)
	}
}

func TestApplyMemoryTuningSetsLimitAndLeavesGC(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "")
	prevLimit := debug.SetMemoryLimit(-1)
	prevGC := debug.SetGCPercent(77)
	t.Cleanup(func() { debug.SetMemoryLimit(prevLimit); debug.SetGCPercent(prevGC) })
	applyMemoryTuning()
	if got := debug.SetMemoryLimit(-1); got < memorySoftLimit {
		t.Errorf("limit %d, want at least %d", got, memorySoftLimit)
	}
	if got := debug.SetGCPercent(77); got != 77 {
		t.Errorf("gc percent changed to %d, it must be left alone", got)
	}
}

// TestTuneMemoryRespectsGOMEMLIMITEnv: a limit the person set in the environment is
// not replaced.
func TestTuneMemoryRespectsGOMEMLIMITEnv(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "123MiB")
	prevLimit := debug.SetMemoryLimit(123 << 20) // what the runtime did at start-up
	t.Cleanup(func() { debug.SetMemoryLimit(prevLimit) })
	applyMemoryTuning()
	if got := debug.SetMemoryLimit(-1); got != 123<<20 {
		t.Errorf("GOMEMLIMIT was set but the limit became %d", got)
	}
}

// TestTuneMemoryRespectsGOGCEnv: the app never sets the GC percent, so a GOGC the
// person set stays whatever the machine is.
func TestTuneMemoryRespectsGOGCEnv(t *testing.T) {
	t.Setenv("GOGC", "33")
	t.Setenv("GOMEMLIMIT", "")
	prevGC := debug.SetGCPercent(33) // what the runtime did at start-up
	prevLimit := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetGCPercent(prevGC); debug.SetMemoryLimit(prevLimit) })
	applyMemoryTuning()
	if got := debug.SetGCPercent(33); got != 33 {
		t.Errorf("GOGC was set but the percent became %d", got)
	}
}
