//go:build !windows && !linux

package perfbench

import "time"

// Off Windows only the Go runtime numbers are available.
func osMem() (ws, peakWS, priv uint64)    { return 0, 0, 0 }
func osCPU() (user, kernel time.Duration) { return 0, 0 }

// wakeups counts nothing off Linux (see leakcheck.Wakeups).
func wakeups() (uint64, bool) { return 0, false }
