//go:build windows

package blackbox

import (
	"math"
	"testing"
)

// TestCPUPctOfOneCore: the budget's CPU unit is percent of ONE core (one full
// core = 100, threshold 0.5), never percent of the whole machine — the helper
// must not divide by the core count, or the CPU budget never fires on a
// multi-core server.
func TestCPUPctOfOneCore(t *testing.T) {
	cases := []struct {
		name          string
		cpuDelta100ns float64
		wallSeconds   float64
		want          float64
	}{
		{"one core-second over one second is 100", 1e7, 1, 100},
		{"half a core-second over one second is 50", 5e6, 1, 50},
		{"one core-second over two seconds is 50", 1e7, 2, 50},
		{"the budget threshold is 0.5 of one core", 5e4, 1, 0.5},
		{"two cores saturated over one second is 200", 2e7, 1, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cpuPctOfOneCore(tc.cpuDelta100ns, tc.wallSeconds)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("cpuPctOfOneCore(%v, %v) = %v, want %v", tc.cpuDelta100ns, tc.wallSeconds, got, tc.want)
			}
		})
	}
}