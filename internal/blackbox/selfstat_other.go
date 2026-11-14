//go:build !windows

package blackbox

import "runtime"

// SelfUsage has no self-measurement off Windows: a missing reading, never
// 0-as-fact.
func SelfUsage() (cpuPctOfOneCore float64, wsBytes uint64, ok bool) { return 0, 0, false }

// SetBelowNormalPriority is a no-op off Windows.
func SetBelowNormalPriority() error { return nil }

// CoreCount is the number of logical cores, at least 1.
func CoreCount() int {
	if n := runtime.NumCPU(); n >= 1 {
		return n
	}
	return 1
}