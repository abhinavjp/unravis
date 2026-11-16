//go:build windows

package bbsql

import (
	"syscall"
	"testing"
	"time"
)

// budgetProcessCPU is this process's kernel+user CPU time. C13's "1% of one
// core" is a claim about the core, not about wall-clock latency, so the gate
// measures CPU here and never the tick's elapsed time. Self only: the
// current-process pseudo-handle, no other process is opened.
func budgetProcessCPU(t *testing.T) time.Duration {
	t.Helper()
	var creation, exit, kernel, user syscall.Filetime
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Skipf("process CPU unavailable: %v", err)
	}
	if err := syscall.GetProcessTimes(proc, &creation, &exit, &kernel, &user); err != nil {
		t.Skipf("process CPU unavailable: %v", err)
	}
	// FILETIME as 100 ns ticks. syscall.Filetime.Nanoseconds is not used: it
	// rebases to the Unix epoch, which is wrong for a duration.
	ticks := int64(kernel.HighDateTime)<<32 | int64(kernel.LowDateTime)
	ticks += int64(user.HighDateTime)<<32 | int64(user.LowDateTime)
	return time.Duration(ticks) * 100
}
