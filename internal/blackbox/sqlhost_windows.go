//go:build windows

package blackbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pdhDetailWizard is PERF_DETAIL_WIZARD: enumerate every object, including
// expert-level ones, so no SQL instance object is filtered out.
const pdhDetailWizard = 400

// sqlEnumPDHObjectsPlatform enumerates the local machine's PDH object names via
// PdhEnumObjectsW (lazy pdh.dll). It is the Windows default of the discovery
// seam in sqlhost.go; off Windows the twin in sqlhost_other.go reports no PDH
// (same build-tag pattern as NewPDHSource). The returned object list is a
// multi-string: NUL-separated names, double NUL at the end.
func sqlEnumPDHObjectsPlatform() ([]string, error) {
	if err := pdh.Load(); err != nil {
		return nil, err
	}
	// Size probe: a NULL buffer asks for the required character count.
	var n uint32
	r, _, _ := procPdhEnumObjects.Call(0, 0, 0, uintptr(unsafe.Pointer(&n)), pdhDetailWizard, 1)
	if r != 0 && r != pdhMoreData {
		return nil, fmt.Errorf("PdhEnumObjects: 0x%x", r)
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]uint16, n)
	// bRefresh=TRUE re-reads the object list so newly installed instances show
	// up without a process restart.
	r, _, _ = procPdhEnumObjects.Call(0, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), pdhDetailWizard, 1)
	if r != 0 {
		return nil, fmt.Errorf("PdhEnumObjects: 0x%x", r)
	}
	var out []string
	for start, i := 0, 0; i < int(n); i++ {
		if buf[i] == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out, nil
}