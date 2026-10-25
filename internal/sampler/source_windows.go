//go:build windows

package sampler

import (
	"errors"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The calls with no wrapper in x/sys/windows go through lazy DLL procs. CGO is
// not used.
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	psapi    = windows.NewLazySystemDLL("psapi.dll")

	procGetSystemTimes        = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx  = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetProcessHandleCount = kernel32.NewProc("GetProcessHandleCount")
	procSetThreadPriority     = kernel32.NewProc("SetThreadPriority")
	procGetCurrentThread      = kernel32.NewProc("GetCurrentThread")

	// psapi exports GetProcessMemoryInfo and GetPerformanceInfo; kernel32 only
	// forwards them under K32-prefixed names.
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
	procGetPerformanceInfo   = psapi.NewProc("GetPerformanceInfo")
)

const (
	processQueryLimitedInformation = 0x1000
	allProcessorGroups             = 0xFFFF

	threadModeBackgroundBegin = 0x00010000
	threadModeBackgroundEnd   = 0x00020000
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// processMemoryCountersEx is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCountersEx struct {
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
	PrivateUsage               uintptr
}

// performanceInformation is PERFORMANCE_INFORMATION; sizes are in pages.
type performanceInformation struct {
	Cb                uint32
	CommitTotal       uintptr
	CommitLimit       uintptr
	CommitPeak        uintptr
	PhysicalTotal     uintptr
	PhysicalAvailable uintptr
	SystemCache       uintptr
	KernelTotal       uintptr
	KernelPaged       uintptr
	KernelNonpaged    uintptr
	PageSize          uintptr
	HandleCount       uint32
	ProcessCount      uint32
	ThreadCount       uint32
}

type winSource struct {
	cores int
}

// OSSource is the Windows Source.
func OSSource() (Source, error) {
	if err := kernel32.Load(); err != nil {
		return nil, err
	}
	if err := psapi.Load(); err != nil {
		return nil, err
	}
	n := int(windows.GetActiveProcessorCount(allProcessorGroups))
	if n < 1 {
		return nil, errors.New("sampler: the number of processors is not known")
	}
	return &winSource{cores: n}, nil
}

func (w *winSource) Cores() int   { return w.cores }
func (w *winSource) Close() error { return nil }

func filetimeUint(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

func (w *winSource) Host() (HostSample, error) {
	var idle, kernel, user windows.Filetime
	if r, _, err := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r == 0 {
		return HostSample{}, err
	}
	var h HostSample
	h.Times = HostTimes{Idle: filetimeUint(idle), Kernel: filetimeUint(kernel), User: filetimeUint(user)}

	ms := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r == 0 {
		return HostSample{}, err
	}
	h.AvailableBytes = ms.AvailPhys

	pi := performanceInformation{Cb: uint32(unsafe.Sizeof(performanceInformation{}))}
	if r, _, err := procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&pi)), uintptr(pi.Cb)); r == 0 {
		return HostSample{}, err
	}
	h.CommittedBytes = uint64(pi.CommitTotal) * uint64(pi.PageSize)
	return h, nil
}

// Processes lists the processes with Toolhelp (name and thread count for every
// process) and reads the rest from each process that can be opened with
// PROCESS_QUERY_LIMITED_INFORMATION. Every handle is closed before the call
// returns.
func (w *winSource) Processes(dst []ProcSample) ([]ProcSample, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return dst, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return dst, err
	}
	for {
		if e.ProcessID != 0 { // the idle pseudo-process
			p := ProcSample{PID: e.ProcessID, Image: windows.UTF16ToString(e.ExeFile[:]), Threads: e.Threads}
			readProcess(&p)
			dst = append(dst, p)
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				return dst, nil
			}
			return dst, err
		}
	}
}

// readProcess fills what the process allows. A process that cannot be opened
// keeps Have 0.
func readProcess(p *ProcSample) {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, p.PID)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		p.Have |= HaveTimes
		p.Start = time.Unix(0, creation.Nanoseconds()).UTC()
		p.CPUSeconds = float64(filetimeUint(kernel)+filetimeUint(user)) / 1e7
	}
	var mc processMemoryCountersEx
	mc.Cb = uint32(unsafe.Sizeof(mc))
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.Cb)); r != 0 {
		p.Have |= HaveMemory
		p.WorkingSet = uint64(mc.WorkingSetSize)
		p.PrivateBytes = uint64(mc.PrivateUsage)
	}
	var handles uint32
	if r, _, _ := procGetProcessHandleCount.Call(uintptr(h), uintptr(unsafe.Pointer(&handles))); r != 0 {
		p.Have |= HaveHandles
		p.Handles = handles
	}
}

// beginBackground puts the calling thread in background mode (low CPU, I/O and
// memory priority). The returned function ends it; call it on the same thread.
func beginBackground() func() {
	cur, _, _ := procGetCurrentThread.Call()
	if r, _, _ := procSetThreadPriority.Call(cur, threadModeBackgroundBegin); r == 0 {
		return func() {} // already in background mode, or not allowed: go on at normal priority
	}
	return func() { procSetThreadPriority.Call(cur, threadModeBackgroundEnd) }
}
