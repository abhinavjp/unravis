//go:build !windows

package blackbox

import "runtime"

// selfHeap reads the Go heap in use; tick-loop only.
var selfHeap = newHeapReader()

// SelfUsage off Windows reads only the Go heap: there is no CPU or private-byte
// reading, so those stay absent (zero), never 0-as-fact.
func SelfUsage() (SelfReading, bool) { return SelfReading{HeapInUse: selfHeap.read()}, true }

// SetBelowNormalPriority is a no-op off Windows.
func SetBelowNormalPriority() error { return nil }

// CoreCount is the number of logical cores, at least 1.
func CoreCount() int {
	if n := runtime.NumCPU(); n >= 1 {
		return n
	}
	return 1
}
