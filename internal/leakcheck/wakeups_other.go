//go:build !linux

package leakcheck

// Wakeups is not counted off Linux.
func Wakeups() (uint64, bool) { return 0, false }
