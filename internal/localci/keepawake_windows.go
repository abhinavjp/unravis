//go:build windows

package localci

import "golang.org/x/sys/windows"

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// setExecutionState holds the system awake (system required, not the display) or releases the hold. The state
// is kept for the calling thread until it is cleared or the thread ends.
func setExecutionState(on bool) {
	flags := uintptr(esContinuous)
	if on {
		flags |= esSystemRequired
	}
	_, _, _ = procSetThreadExecutionState.Call(flags)
}
