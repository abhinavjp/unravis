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
		{0, 256 * mib}, // unknown RAM: the floor
		{1 * gib, 256 * mib},
		{2 * gib, 512 * mib}, // a quarter of 2 GiB is exactly the cap
		{4 * gib, 512 * mib},
		{8 * gib, 512 * mib},
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
