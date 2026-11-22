//go:build linux

package leakcheck

import (
	"bytes"
	"os"
	"strconv"
)

// Wakeups sums the context switches of every thread of this process. A thread
// that sleeps and is woken (a timer, a netpoll return, a futex) counts one
// voluntary switch, so the change over an idle window is the Linux stand-in for
// "timer wakeups per minute". ok is false when /proc cannot be read.
func Wakeups() (n uint64, ok bool) {
	dirs, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return 0, false
	}
	for _, d := range dirs {
		b, err := os.ReadFile("/proc/self/task/" + d.Name() + "/status")
		if err != nil {
			continue
		}
		n += ctxt(b, "voluntary_ctxt_switches") + ctxt(b, "nonvoluntary_ctxt_switches")
	}
	return n, true
}

func ctxt(status []byte, name string) uint64 {
	for _, line := range bytes.Split(status, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(name+":")) {
			n, _ := strconv.ParseUint(string(bytes.TrimSpace(line[len(name)+1:])), 10, 64)
			return n
		}
	}
	return 0
}
