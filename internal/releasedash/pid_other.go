//go:build !windows

package releasedash

import (
	"os"
	"syscall"
	"time"
)

func pidAlive(pid int, _ time.Time) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
