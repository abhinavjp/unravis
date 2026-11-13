//go:build windows

package blackbox

import (
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procGetProcessMemoryInfo is K32GetProcessMemoryInfo (kernel32, Windows 7+);
// x/sys does not wrap it. It is only ever called with the current-process
// pseudo-handle: self only, never another process.
var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS; only workingSetSize is read.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// selfStat keeps the previous CPU times and wall time for the delta between
// ticks. Tick-loop only: no mutex, no goroutine, O(1) per call.
var selfStat struct {
	haveLast bool
	lastCPU  uint64 // kernel+user, 100 ns ticks
	lastWall time.Time
}

// SelfUsage reads the Blackbox's own process only (the current-process
// pseudo-handle — no other process, no attach, no dump). cpuPctOfOneCore is
// the kernel+user delta since the last call / wall delta / cores * 100;
// wsBytes is the working set. ok=false on any failure or on the first call
// (no delta yet): a missing reading is omitted, never 0-as-fact.
func SelfUsage() (cpuPctOfOneCore float64, wsBytes uint64, ok bool) {
	h := windows.CurrentProcess()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, 0, false
	}
	var mem processMemoryCounters
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mem)), unsafe.Sizeof(mem)); r == 0 {
		return 0, 0, false
	}
	cpu := filetimeTicks(kernel) + filetimeTicks(user)
	now := time.Now()
	prevCPU, prevWall, have := selfStat.lastCPU, selfStat.lastWall, selfStat.haveLast
	selfStat.haveLast, selfStat.lastCPU, selfStat.lastWall = true, cpu, now
	if !have || cpu < prevCPU {
		return 0, 0, false
	}
	dWall := now.Sub(prevWall).Seconds()
	if dWall <= 0 {
		return 0, 0, false
	}
	pct := float64(cpu-prevCPU) / 1e7 / dWall / float64(CoreCount()) * 100
	return pct, uint64(mem.workingSetSize), true
}

// filetimeTicks is a FILETIME as 100 ns ticks.
func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// SetBelowNormalPriority lowers this process to below-normal priority. It
// only ever lowers, never raises, and touches only the current process.
func SetBelowNormalPriority() error {
	return windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)
}

// CoreCount is the number of logical cores, at least 1.
func CoreCount() int {
	if n := runtime.NumCPU(); n >= 1 {
		return n
	}
	return 1
}