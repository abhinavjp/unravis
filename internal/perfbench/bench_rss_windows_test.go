package perfbench

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCountersEx is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// osMem returns this process's working set, peak working set and private bytes.
func osMem() (ws, peakWS, priv uint64) {
	var c processMemoryCountersEx
	c.CB = uint32(unsafe.Sizeof(c))
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, 0, 0
	}
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return 0, 0, 0
	}
	return uint64(c.WorkingSetSize), uint64(c.PeakWorkingSetSize), uint64(c.PrivateUsage)
}

// osCPU returns this process's user and kernel CPU time so far.
func osCPU() (user, kernel time.Duration) {
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, 0
	}
	var creation, exit, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &k, &u); err != nil {
		return 0, 0
	}
	ft := func(f windows.Filetime) time.Duration {
		return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100
	}
	return ft(u), ft(k)
}
