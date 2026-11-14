//go:build windows

package blackbox

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processQueryLimitedInformation is the only access right used: enough for
// GetProcessTimes, never VM_READ/write, never suspend. Module enumeration uses
// a read-only toolhelp snapshot, not an open of the target.
const processQueryLimitedInformation = 0x1000

// snapshotProcesses enumerates processes with Toolhelp and reads each creation
// time with a query-only open. A process that cannot be opened keeps Start 0
// (unknown); it is still listed with PID and Name. No modules are read here.
func snapshotProcesses() ([]ProcInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, err
	}
	var procs []ProcInfo
	for {
		if e.ProcessID != 0 { // skip the idle pseudo-process
			procs = append(procs, ProcInfo{
				PID:       e.ProcessID,
				ParentPID: e.ParentProcessID,
				Start:     processStart(e.ProcessID),
				Name:      windows.UTF16ToString(e.ExeFile[:]),
			})
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				return procs, nil
			}
			return nil, err
		}
	}
}

// processStart reads the creation time as unix seconds. Query-only open; every
// handle is closed. A process that cannot be opened or queried returns 0.
func processStart(pid uint32) int64 {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return creation.Nanoseconds() / 1_000_000_000
}

// loadModules returns the loaded module base names via a read-only toolhelp
// module snapshot (TH32CS_SNAPMODULE|TH32CS_SNAPMODULE32 covers 32-bit targets
// from a 64-bit service). It never opens, attaches, suspends or dumps the
// target. On access denied it returns an error; the caller leaves runtime 0.
func loadModules(pid uint32) ([]string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var m windows.ModuleEntry32
	m.Size = uint32(unsafe.Sizeof(m))
	if err := windows.Module32First(snap, &m); err != nil {
		return nil, err
	}
	var mods []string
	for {
		mods = append(mods, windows.UTF16ToString(m.Module[:]))
		if err := windows.Module32Next(snap, &m); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				return mods, nil
			}
			return nil, err
		}
	}
}
