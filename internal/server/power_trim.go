package server

import (
	"runtime/debug"
	"sync/atomic"
	"time"
)

// memoryTrimEvery is the least time between two forced memory returns to the system (BL-425).
const memoryTrimEvery = 30 * time.Second

// memoryTrimmer returns freed memory to the system after a slow request, but at most once per interval, so a burst of slow
// requests that finish together does not run a forced collection for each of them. The zero value works; the fields are for tests.
type memoryTrimmer struct {
	last  atomic.Int64 // unix nanoseconds of the last run; 0 = never
	every time.Duration
	now   func() time.Time
	free  func()
}

// maybe runs the trim when none ran in the last interval and reports whether it did.
func (m *memoryTrimmer) maybe() bool {
	every, now, free := m.every, m.now, m.free
	if every <= 0 {
		every = memoryTrimEvery
	}
	if now == nil {
		now = time.Now
	}
	if free == nil {
		free = debug.FreeOSMemory
	}
	at := now().UnixNano()
	for {
		last := m.last.Load()
		if last != 0 && at-last < int64(every) {
			return false
		}
		if m.last.CompareAndSwap(last, at) {
			break
		}
	}
	free()
	return true
}
