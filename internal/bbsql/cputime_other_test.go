//go:build !windows

package bbsql

import (
	"syscall"
	"testing"
	"time"
)

// budgetProcessCPU is this process's kernel+user CPU time (RUSAGE_SELF: never a
// child, never another process). C13's "1% of one core" is a claim about the
// core, not about wall-clock latency, so the gate measures CPU here.
func budgetProcessCPU(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Skipf("process CPU unavailable: %v", err)
	}
	user := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	sys := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return user + sys
}
