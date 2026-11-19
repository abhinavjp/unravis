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
var (
	kernel32Self               = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessMemoryInfo   = kernel32Self.NewProc("K32GetProcessMemoryInfo")
	procGetProcessHandleCountS = kernel32Self.NewProc("GetProcessHandleCount")
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS. pagefileUsage is the
// process's private (committed) bytes on current Windows.
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

// selfHeap reads the Go heap in use; tick-loop only.
var selfHeap = newHeapReader()

// SelfUsage reads the Blackbox's own process only (the current-process
// pseudo-handle: no other process, no attach, no dump). CPUPct is the
// kernel+user delta since the last call over the wall delta, in percent of ONE
// core. It is absent (zero WallSeconds) on the first call: no delta yet. Heap,
// private bytes and handle count are read every call. ok=false on a failed
// process read: a missing reading is omitted, never 0-as-fact.
func SelfUsage() (SelfReading, bool) {
	h := windows.CurrentProcess()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return SelfReading{}, false
	}
	var mem processMemoryCounters
	mem.cb = uint32(unsafe.Sizeof(mem))
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mem)), unsafe.Sizeof(mem)); r == 0 {
		return SelfReading{}, false
	}
	out := SelfReading{HeapInUse: selfHeap.read(), Private: uint64(mem.pagefileUsage)}
	var handles uint32
	if r, _, _ := procGetProcessHandleCountS.Call(uintptr(h), uintptr(unsafe.Pointer(&handles))); r != 0 {
		out.Handles = handles
	}
	cpu := filetimeTicks(kernel) + filetimeTicks(user)
	now := time.Now()
	prevCPU, prevWall, have := selfStat.lastCPU, selfStat.lastWall, selfStat.haveLast
	selfStat.haveLast, selfStat.lastCPU, selfStat.lastWall = true, cpu, now
	if have && cpu >= prevCPU {
		if dWall := now.Sub(prevWall).Seconds(); dWall > 0 {
			out.CPUPct = cpuPctOfOneCore(float64(cpu-prevCPU), dWall)
			out.WallSeconds = dWall
		}
	}
	return out, true
}

// cpuPctOfOneCore is the CPU percent of ONE core: cpuDelta100ns of kernel+user
// time (100 ns ticks) spent over wallSeconds of wall time. One full core for
// the whole wall second is 100. There is deliberately NO division by the core
// count: the budget judges against 1.0% of one core, so dividing by
// cores (percent of the machine) would hide an over-budget recorder on any
// multi-core server.
func cpuPctOfOneCore(cpuDelta100ns, wallSeconds float64) float64 {
	return cpuDelta100ns / 1e7 / wallSeconds * 100
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
