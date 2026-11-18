package blackbox

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSelfTrackerKeepsLatestAndPeaks(t *testing.T) {
	var tr selfTracker
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

	st := tr.note(now, SelfReading{HeapInUse: 20 << 20, Private: 80 << 20, Handles: 300}, true, 12*time.Millisecond)
	if st.HeapMB != 20 || st.PrivateMB != 80 || st.Handles != 300 || st.TickMs != 12 {
		t.Fatalf("first: %+v", st)
	}
	st = tr.note(now.Add(time.Minute), SelfReading{HeapInUse: 10 << 20, Private: 60 << 20, Handles: 250}, true, 5*time.Millisecond)
	if st.HeapMB != 10 || st.PeakHeapMB != 20 || st.PeakPrivateMB != 80 || st.PeakHandles != 300 || st.PeakTickMs != 12 || st.TickMs != 5 {
		t.Fatalf("second: %+v", st)
	}
	// A missing reading keeps what was there and never records zero as a peak or a value.
	st = tr.note(now.Add(2*time.Minute), SelfReading{}, false, 7*time.Millisecond)
	if st.HeapMB != 10 || st.PrivateMB != 60 || st.Handles != 250 || st.TickMs != 7 {
		t.Fatalf("missing reading changed the values: %+v", st)
	}
	if !st.Updated.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("updated = %v", st.Updated)
	}
}

func TestSelfStatsJSONIsAdditive(t *testing.T) {
	st := SelfStats{HeapMB: 12.5, TickMs: 3, Updated: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"heapMB":12.5`, `"tickMs":3`, `"updated"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("%s missing from %s", want, s)
		}
	}
	// Fields not read are left out, not written as zero.
	for _, not := range []string{"privateMB", "handles", "peakPrivateMB", "peakHandles"} {
		if strings.Contains(s, not) {
			t.Fatalf("%s must be omitted when not read: %s", not, s)
		}
	}
}

func TestRound1(t *testing.T) {
	cases := map[float64]float64{0: 0, 1.04: 1, 1.05: 1.1, 12.34: 12.3, -3: 0}
	for in, want := range cases {
		if got := round1(in); got != want {
			t.Errorf("round1(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestHeapReaderReadsAndAllocatesNothing(t *testing.T) {
	h := newHeapReader()
	if h.read() == 0 {
		t.Skip("runtime/metrics heap objects not available")
	}
	if n := testing.AllocsPerRun(100, func() { h.read() }); n != 0 {
		t.Fatalf("heapReader.read allocated %v per call", n)
	}
}

// SelfUsage reads this process only: memory and handles are real numbers, and
// the CPU figure (a delta against the previous call, kept for the process) is
// never negative.
func TestSelfUsageReadsThisProcess(t *testing.T) {
	r, ok := SelfUsage()
	if !ok {
		t.Skip("no self reading on this platform")
	}
	if r.Private == 0 || r.Handles == 0 {
		t.Fatalf("private bytes or handles not read: %+v", r)
	}
	time.Sleep(30 * time.Millisecond)
	r2, ok := SelfUsage()
	if !ok {
		t.Fatal("second reading missing")
	}
	if r2.CPUPct < 0 || r2.WallSeconds < 0 {
		t.Fatalf("negative figures: %+v", r2)
	}
	if r2.WallSeconds == 0 {
		t.Fatalf("second reading has no wall time for its CPU delta: %+v", r2)
	}
}
