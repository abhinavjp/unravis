package blackbox

import (
	"runtime/metrics"
	"time"
)

// SelfReading is one reading of the Blackbox's own cost. It is the input of the
// light budget and the source of the manifest's self block.
//
// CPUPct is percent of ONE core (one full core = 100, no core-count division)
// averaged over WallSeconds, the time since the previous reading. HeapInUse is
// the Go heap in use (live objects plus garbage not yet collected). Private is
// the process's private (committed) bytes. A field that could not be read is 0
// and is never counted as a reading.
type SelfReading struct {
	CPUPct      float64
	WallSeconds float64
	HeapInUse   uint64
	Private     uint64
	Handles     uint32
}

// SelfStats is the self block of a run record in the manifest: how much the
// Blackbox itself costs, with the peaks of the run. All fields are optional and
// additive, so a reader of an older manifest still works. Sizes are in MB and
// tick time is in milliseconds, rounded to one decimal.
type SelfStats struct {
	HeapMB        float64 `json:"heapMB"`
	PrivateMB     float64 `json:"privateMB,omitempty"`
	Handles       uint32  `json:"handles,omitempty"`
	TickMs        float64 `json:"tickMs"`
	PeakHeapMB    float64 `json:"peakHeapMB,omitempty"`
	PeakPrivateMB float64 `json:"peakPrivateMB,omitempty"`
	PeakHandles   uint32  `json:"peakHandles,omitempty"`
	PeakTickMs    float64 `json:"peakTickMs,omitempty"`
	// Where the last tick went, so a heavy tick can be traced without a debugger:
	// the self reading, the host read, the slowest group (and its name), and the
	// file write. All optional and additive.
	FeedMs      float64   `json:"feedMs,omitempty"`
	HostMs      float64   `json:"hostMs,omitempty"`
	SlowGroup   string    `json:"slowGroup,omitempty"`
	SlowGroupMs float64   `json:"slowGroupMs,omitempty"`
	AppendMs    float64   `json:"appendMs,omitempty"`
	Updated     time.Time `json:"updated"`
}

const mib = 1 << 20

func toMB(b uint64) float64 { return round1(float64(b) / mib) }

func round1(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int64(v*10+0.5)) / 10
}

// heapReader reads the Go heap in use from runtime/metrics. The sample slice is
// reused, so a read costs no allocation. Tick-loop only.
type heapReader struct {
	sample []metrics.Sample
}

func newHeapReader() *heapReader {
	return &heapReader{sample: []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}}
}

// read returns the bytes of heap objects (live plus not yet swept), 0 when the
// metric is not available.
func (h *heapReader) read() uint64 {
	metrics.Read(h.sample)
	if h.sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return h.sample[0].Value.Uint64()
}

// selfTracker keeps the running self block of this run: the latest reading and
// the peaks. Tick-loop only; the store receives a copy.
type selfTracker struct {
	cur SelfStats
}

// note folds one reading and the last tick's duration into the block.
func (t *selfTracker) note(now time.Time, r SelfReading, haveReading bool, tick time.Duration) SelfStats {
	c := &t.cur
	if haveReading {
		if r.HeapInUse > 0 {
			c.HeapMB = toMB(r.HeapInUse)
			if c.HeapMB > c.PeakHeapMB {
				c.PeakHeapMB = c.HeapMB
			}
		}
		if r.Private > 0 {
			c.PrivateMB = toMB(r.Private)
			if c.PrivateMB > c.PeakPrivateMB {
				c.PeakPrivateMB = c.PrivateMB
			}
		}
		if r.Handles > 0 {
			c.Handles = r.Handles
			if r.Handles > c.PeakHandles {
				c.PeakHandles = r.Handles
			}
		}
	}
	if tick > 0 {
		c.TickMs = round1(float64(tick) / float64(time.Millisecond))
		if c.TickMs > c.PeakTickMs {
			c.PeakTickMs = c.TickMs
		}
	}
	c.Updated = now.UTC()
	return *c
}
