//go:build windows

package mcp

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorInsufficientBuffer        = syscall.Errno(122)
	afInet                         = 2
	tcpTableOwnerPIDListener       = 3
	tcpRowOwnerPIDBytes            = 24
)

var procGetExtendedTCPTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// processAlive reports whether pid names a running process. A process this
// user may not query (access denied) exists, so it counts as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == stillActive
}

// listenerOwners returns the processes listening on 127.0.0.1:port or
// 0.0.0.0:port over IPv4. ok is false when the table could not be read.
func listenerOwners(port int) (owners []int, ok bool) {
	if procGetExtendedTCPTable.Find() != nil {
		return nil, false
	}
	size := uint32(16 << 10)
	var table []byte
	for attempt := 0; attempt < 4; attempt++ {
		table = make([]byte, size)
		r, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&table[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListener, 0)
		if r == 0 {
			break
		}
		if syscall.Errno(r) != errorInsufficientBuffer {
			return nil, false
		}
		size += 4 << 10
		table = nil
	}
	if len(table) < 4 {
		return nil, false
	}
	count := int(binary.LittleEndian.Uint32(table))
	if count < 0 || 4+count*tcpRowOwnerPIDBytes > len(table) {
		return nil, false
	}
	for i := 0; i < count; i++ {
		row := table[4+i*tcpRowOwnerPIDBytes:]
		local := binary.LittleEndian.Uint32(row[4:])
		rowPort := int(row[8])<<8 | int(row[9])
		if rowPort != port || (local != 0 && local != 0x0100007f) {
			continue
		}
		owners = append(owners, int(binary.LittleEndian.Uint32(row[20:])))
	}
	return owners, true
}
