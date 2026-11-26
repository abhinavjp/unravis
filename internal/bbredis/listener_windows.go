//go:build windows

package bbredis

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UNVERIFIED on a live server: compiles (GOOS=windows go vet) and the pure parts are tested, but the table and token
// calls have not run on a real Windows host yet.

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	afInet                   = 2
	tcpTableOwnerPidListener = 3
	errInsufficientBuffer    = 122
)

// tcpRowOwnerPID is MIB_TCPROW_OWNER_PID.
type tcpRowOwnerPID struct {
	State, LocalAddr, LocalPort, RemoteAddr, RemotePort, OwningPid uint32
}

// listenerPIDs returns the owner of every IPv4 listener that can take a connection to 127.0.0.1:port (bound to
// 127.0.0.1 or to all addresses). More than one is possible on Windows (shared binds), so all must be checked.
func listenerPIDs(port int) ([]uint32, error) {
	size := uint32(0)
	for try := 0; try < 5; try++ {
		var buf []byte
		var p unsafe.Pointer
		if size > 0 {
			buf = make([]byte, size)
			p = unsafe.Pointer(&buf[0])
		}
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(p), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
		if r == errInsufficientBuffer {
			size += 4096 // the table can grow between the two calls
			continue
		}
		if r != 0 {
			return nil, syscall.Errno(r)
		}
		if len(buf) < 4 {
			return nil, ErrUntrustedListener
		}
		n := int(*(*uint32)(p))
		const rowSize = 24
		if n < 0 || 4+n*rowSize > len(buf) {
			return nil, ErrUntrustedListener
		}
		var pids []uint32
		for i := 0; i < n; i++ {
			row := (*tcpRowOwnerPID)(unsafe.Add(p, 4+i*rowSize))
			lp := int(row.LocalPort&0xff)<<8 | int(row.LocalPort>>8&0xff) // network byte order
			if lp != port {
				continue
			}
			if row.LocalAddr == 0 || row.LocalAddr == 0x0100007f { // 0.0.0.0 or 127.0.0.1
				pids = append(pids, row.OwningPid)
			}
		}
		return pids, nil
	}
	return nil, ErrUntrustedListener
}

// pidTrusted is true for a service account (SYSTEM, LocalService, NetworkService) or a program whose file is under a
// Program Files folder, which only an administrator can write to.
func pidTrusted(pid uint32) bool {
	if pid <= 4 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err == nil {
		defer tok.Close()
		if u, err := tok.GetTokenUser(); err == nil && u.User.Sid != nil {
			if u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) || u.User.Sid.IsWellKnown(windows.WinLocalServiceSid) ||
				u.User.Sid.IsWellKnown(windows.WinNetworkServiceSid) {
				return true
			}
		}
	}
	var name [windows.MAX_LONG_PATH]uint16
	n := uint32(len(name))
	if err := windows.QueryFullProcessImageName(h, 0, &name[0], &n); err != nil {
		return false
	}
	image := windows.UTF16ToString(name[:n])
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86} {
		if dir, err := windows.KnownFolderPath(id, 0); err == nil && pathUnder(image, dir) {
			return true
		}
	}
	return false
}

func listenerTrusted(port int) error {
	pids, err := listenerPIDs(port)
	if err != nil || len(pids) == 0 {
		return ErrUntrustedListener // nobody listening any more, or the table could not be read: send nothing
	}
	for _, pid := range pids {
		if !pidTrusted(pid) {
			return ErrUntrustedListener
		}
	}
	return nil
}
