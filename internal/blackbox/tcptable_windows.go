//go:build windows

package blackbox

import (
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
// into any struct. No handle is held between calls; no goroutine. The byte
// ceiling is checked BEFORE allocation and retries are bounded (W6-03) by
// tcpTableSnapshot; the native calls are the injected seams below.
func tcpTableCounts() (tcpStates, error) {
	if err := iphlpapi.Load(); err != nil {
		return tcpStates{}, err
	}
	return tcpTableSnapshot(tcpNativeSize, tcpNativeRead)
}

// tcpNativeSize asks GetTcpTable2 for the table's byte size (no allocation).
func tcpNativeSize() (uint32, error) {
	var size uint32
	r, _, _ := procGetTcpTable2.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 && r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return 0, errTCPTableUnreadable
	}
	return size, nil
}

// tcpNativeRead fills buf with the table. A too-small buffer (the table grew)
// is errTCPTableGrew so the snapshot helper retries within its bound.
func tcpNativeRead(buf []byte) (uint32, error) {
	size := uint32(len(buf))
	r, _, _ := procGetTcpTable2.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return 0, errTCPTableGrew
	}
	if r != 0 {
		return 0, errTCPTableUnreadable
	}
	return size, nil
}
