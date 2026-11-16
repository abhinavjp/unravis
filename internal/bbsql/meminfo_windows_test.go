//go:build windows

package bbsql

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters mirrors the base Win32 PROCESS_MEMORY_COUNTERS
// (psapi.h): two DWORDs then ten SIZE_Ts. The _EX fields are not asked for -
// cb only covers this struct, which is all the working set needs. It is
// declared here because x/sys/windows imports psapi but has no type for it.
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var (
	psapi               = windows.NewLazySystemDLL("psapi.dll")
	k32GetProcessMemory = psapi.NewProc("K32GetProcessMemoryInfo")
	oldGetProcessMemory = psapi.NewProc("GetProcessMemoryInfo")
)

// budgetWorkingSetBytes is this process's real working set (RSS), not a Go
// heap counter: C13's "under 10 MB extra memory" is a claim about the memory
// the service holds on the server. ok=false when psapi cannot answer, and the
// caller skips rather than passing on a guess. Current process only.
func budgetWorkingSetBytes(t *testing.T) (int64, bool) {
	t.Helper()
	proc := k32GetProcessMemory
	if err := proc.Find(); err != nil {
		proc = oldGetProcessMemory
		if err := proc.Find(); err != nil {
			return 0, false
		}
	}
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	pmc := processMemoryCounters{Cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	r1, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb))
	if r1 == 0 {
		return 0, false
	}
	return int64(pmc.WorkingSetSize), true
}
