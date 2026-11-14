//go:build windows

package blackbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi         = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetTcpTable2 = iphlpapi.NewProc("GetTcpTable2")
)

// tcpTableCounts snapshots the TCP table via GetTcpTable2 and counts rows by
// dwState. The raw buffer goes straight to tcpStateCountsFromTable, which
// reads only the state DWORD: addresses, ports and PIDs are never copied
// into any struct. No handle is held between calls; no goroutine.
func tcpTableCounts() ([]TCPStateCount, error) {
	if err := iphlpapi.Load(); err != nil {
		return nil, err
	}
	var size uint32
	r, _, _ := procGetTcpTable2.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 && r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, fmt.Errorf("GetTcpTable2 size: 0x%x", r)
	}
	if size < 4 {
		return nil, nil // empty table
	}
	buf := make([]byte, size)
	r, _, _ = procGetTcpTable2.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 {
		// Includes a table that outgrew the size call: States nil this tick.
		return nil, fmt.Errorf("GetTcpTable2: 0x%x", r)
	}
	return tcpStateCountsFromTable(buf), nil
}