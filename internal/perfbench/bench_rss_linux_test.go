//go:build linux

package perfbench

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
	"time"

	"api-audit-explorer/internal/leakcheck"
)

// procKiB reads one "Name:   123 kB" line of /proc/self/status, in bytes.
func procKiB(status []byte, name string) uint64 {
	for _, line := range bytes.Split(status, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte(name+":")) {
			continue
		}
		f := bytes.Fields(line[len(name)+1:])
		if len(f) == 0 {
			return 0
		}
		n, _ := strconv.ParseUint(string(f[0]), 10, 64)
		return n << 10
	}
	return 0
}

// osMem returns this process's resident set, peak resident set and anonymous
// (private) bytes: the Linux counterparts of working set, peak working set and
// private bytes.
func osMem() (ws, peakWS, priv uint64) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0, 0
	}
	return procKiB(b, "VmRSS"), procKiB(b, "VmHWM"), procKiB(b, "RssAnon")
}

func osCPU() (user, kernel time.Duration) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	return time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano())
}

// wakeups counts this process's context switches (see leakcheck.Wakeups).
func wakeups() (uint64, bool) { return leakcheck.Wakeups() }
