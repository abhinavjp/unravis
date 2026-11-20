//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// ownsConsole reports whether this process is the only one on its console: started by a double-click or a shortcut,
// so the window closes when it exits. From a command prompt the shell shares the console and there is nothing to hold open.
func ownsConsole() bool {
	var ids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return n == 1
}
