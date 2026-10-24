//go:build !windows

package mcp

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid names a running process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// listenerOwners is not implemented outside Windows; the session proof still
// guards the token there.
func listenerOwners(int) ([]int, bool) { return nil, false }
