//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Reuses the lazy pdh.dll (extras_windows.go), pdhMoreData (pdh_windows.go)
// and procPdhEnumObjects (sqlhost_windows.go); only the items proc is new.
var procPdhEnumObjectItems = pdh.NewProc("PdhEnumObjectItemsW")

const pdhDetailWizard = 400 // PERF_DETAIL_WIZARD: enumerate every detail level

// enumW3WPInstances lists the W3SVC_W3WP counter instances ("<PID>_<AppPool>").
// The PDH object cache is refreshed FIRST (PdhEnumObjectsW, bRefresh=TRUE):
// per MSDN, PdhEnumObjectItemsW otherwise returns a stale cached list, so a
// recycled pool's old <PID>_<Pool> instance could mis-target a reused PID.
// Only instance names are kept; no process is opened, no command line is read.
func enumW3WPInstances() ([]string, error) {
	if err := pdh.Load(); err != nil {
		return nil, err
	}
	if err := refreshW3WPObjects(); err != nil {
		return nil, err
	}
	return enumW3WPItems()
}

// The two PDH steps are seams so tests can fake them without live counters.
var (
	refreshW3WPObjects = refreshW3WPObjectCache
	enumW3WPItems      = enumW3WPObjectItems
)

// refreshW3WPObjectCache rebuilds the PDH object cache (all objects, including
// W3SVC_W3WP) via PdhEnumObjectsW with bRefresh=TRUE: one size call, one
// buffered call, both refreshing so the buffered read cannot see a stale
// cache. The object list itself is discarded; only the refresh side effect is
// wanted.
func refreshW3WPObjectCache() error {
	var n uint32
	r, _, _ := procPdhEnumObjects.Call(0, 0, 0, uintptr(unsafe.Pointer(&n)), pdhDetailWizard, 1)
	if r != 0 && r != pdhMoreData {
		return fmt.Errorf("PdhEnumObjectsW: 0x%x", r)
	}
	if n == 0 {
		return nil
	}
	buf := make([]uint16, n)
	r, _, _ = procPdhEnumObjects.Call(0, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), pdhDetailWizard, 1)
	if r != 0 {
		return fmt.Errorf("PdhEnumObjectsW: 0x%x", r)
	}
	return nil
}

// enumW3WPObjectItems lists the instances via PdhEnumObjectItemsW: one size
// call, one buffered call. The caller refreshes the object cache first.
func enumW3WPObjectItems() ([]string, error) {
	obj, err := windows.UTF16PtrFromString("W3SVC_W3WP")
	if err != nil {
		return nil, err
	}
	var nCounters, nInst uint32
	r, _, _ := procPdhEnumObjectItems.Call(0, 0, uintptr(unsafe.Pointer(obj)),
		0, uintptr(unsafe.Pointer(&nCounters)), 0, uintptr(unsafe.Pointer(&nInst)), pdhDetailWizard, 0)
	if r != 0 && r != pdhMoreData {
		return nil, fmt.Errorf("PdhEnumObjectItemsW: 0x%x", r)
	}
	if nInst == 0 {
		return nil, nil
	}
	inst := make([]uint16, nInst)
	var cPtr uintptr
	var counters []uint16
	if nCounters > 0 {
		counters = make([]uint16, nCounters)
		cPtr = uintptr(unsafe.Pointer(&counters[0]))
	}
	r, _, _ = procPdhEnumObjectItems.Call(0, 0, uintptr(unsafe.Pointer(obj)),
		cPtr, uintptr(unsafe.Pointer(&nCounters)), uintptr(unsafe.Pointer(&inst[0])),
		uintptr(unsafe.Pointer(&nInst)), pdhDetailWizard, 0)
	if r != 0 {
		return nil, fmt.Errorf("PdhEnumObjectItemsW: 0x%x", r)
	}
	var out []string // multi-string: NUL-separated names, double NUL at the end
	for start, i := 0, 0; i < int(nInst); i++ {
		if inst[i] == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(inst[start:i]))
			}
			start = i + 1
		}
	}
	return out, nil
}

// enumServiceProcs lists RUNNING win32 services with their PIDs via
// EnumServicesStatusEx (winenv_windows.go pattern). Only names and PIDs are
// read — never binary paths or command lines.
func enumServiceProcs() ([]svcProc, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(m)
	var out []svcProc
	var resume uint32
	buf := make([]byte, 64<<10)
	for {
		var needed, returned uint32
		err := windows.EnumServicesStatusEx(m, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, &buf[0], uint32(len(buf)), &needed, &returned, &resume, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, err
		}
		if returned > 0 {
			entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned))
			for _, e := range entries {
				if e.ServiceStatusProcess.CurrentState == windows.SERVICE_RUNNING && e.ServiceStatusProcess.ProcessId != 0 {
					out = append(out, svcProc{name: windows.UTF16PtrToString(e.ServiceName), pid: e.ServiceStatusProcess.ProcessId})
				}
			}
		}
		runtime.KeepAlive(buf)
		if err == nil {
			return out, nil
		}
		if returned == 0 && needed > uint32(len(buf)) {
			buf = make([]byte, needed) // buffer too small for even one entry
		}
	}
}
