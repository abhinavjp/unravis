//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// Under -ldflags -H=windowsgui Windows hands the process NULL or invalid
// standard handles unless the parent passed some. These helpers give a
// terminal-started copy its console back without touching handles a parent
// passed as pipes (the MCP client, the e2e launcher, setup's --version check).

// procAttachConsole is AttachConsole; x/sys/windows does not wrap it.
var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentProcess is ATTACH_PARENT_PROCESS.
const attachParentProcess = ^uint32(0)

// stdHandleUsable reports whether a standard handle can be written: not NULL,
// not invalid and of a known type (a disk file, a pipe or a character device
// such as a console or NUL).
func stdHandleUsable(h windows.Handle) bool {
	if h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, err := windows.GetFileType(h)
	if err != nil {
		return false
	}
	return t != windows.FILE_TYPE_UNKNOWN
}

// unusableStdHandles marks which of stdin, stdout and stderr (in that order)
// cannot be used: its handle could not be read, or usable says no.
func unusableStdHandles(get func(id uint32) (windows.Handle, error), usable func(windows.Handle) bool) (unusable [3]bool) {
	ids := [3]uint32{windows.STD_INPUT_HANDLE, windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE}
	for i, id := range ids {
		h, err := get(id)
		unusable[i] = err != nil || !usable(h)
	}
	return unusable
}

// attachConsoleWith is attachParentConsole with the Windows calls injected. It
// does not attach when every handle is usable; otherwise it attaches and
// replaces each unusable handle, and reports whether at least one was replaced.
func attachConsoleWith(unusable [3]bool, attach func() bool, replace func(index int) bool) bool {
	if !unusable[0] && !unusable[1] && !unusable[2] {
		return false
	}
	if !attach() {
		return false
	}
	replaced := false
	for i, bad := range unusable {
		if bad && replace(i) {
			replaced = true
		}
	}
	return replaced
}

// attachParentConsole gives a process started from a terminal its console back
// (printing and reading work again). A usable standard handle is never
// replaced. It reports whether any handle was replaced.
func attachParentConsole() bool {
	attach := func() bool {
		r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess))
		return r != 0
	}
	return attachConsoleWith(unusableStdHandles(windows.GetStdHandle, stdHandleUsable), attach, replaceStdHandle)
}

// replaceStdHandle opens CONIN$ (index 0) or CONOUT$ (1 and 2) and makes it the
// standard handle and the matching os file. On failure the handle stays as it was.
func replaceStdHandle(index int) bool {
	ids := [3]uint32{windows.STD_INPUT_HANDLE, windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE}
	name, access := "CONOUT$", uint32(windows.GENERIC_READ|windows.GENERIC_WRITE)
	if index == 0 {
		name, access = "CONIN$", windows.GENERIC_READ
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	if err := windows.SetStdHandle(ids[index], h); err != nil {
		windows.CloseHandle(h)
		return false
	}
	f := os.NewFile(uintptr(h), name)
	switch index {
	case 0:
		os.Stdin = f
	case 1:
		os.Stdout = f
	default:
		os.Stderr = f
	}
	return true
}
