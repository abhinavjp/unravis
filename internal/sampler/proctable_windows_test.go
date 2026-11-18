//go:build windows

package sampler

import (
	"os"
	"testing"
	"time"
)

func newTestWinSource(t testing.TB) *winSource {
	t.Helper()
	src, err := OSSource()
	if err != nil {
		t.Fatal(err)
	}
	return src.(*winSource)
}

// The system-wide read must say the same thing about this process as the
// per-process queries it replaces.
func TestProcessTableMatchesPerProcessQueries(t *testing.T) {
	w := newTestWinSource(t)
	var fast []ProcSample
	fast, err := w.table.appendTo(fast, time.Now().Add(-time.Hour)) // never reused
	if err != nil {
		t.Fatalf("system process read: %v", err)
	}
	slow, err := w.processesToolhelp(nil)
	if err != nil {
		t.Fatalf("toolhelp: %v", err)
	}
	find := func(rows []ProcSample, pid uint32) *ProcSample {
		for i := range rows {
			if rows[i].PID == pid {
				return &rows[i]
			}
		}
		return nil
	}
	me := uint32(os.Getpid())
	a, b := find(fast, me), find(slow, me)
	if a == nil || b == nil {
		t.Fatalf("own pid %d missing: fast=%v slow=%v", me, a != nil, b != nil)
	}
	if a.Image != b.Image {
		t.Errorf("image %q vs %q", a.Image, b.Image)
	}
	if a.Have != HaveTimes|HaveMemory|HaveHandles {
		t.Errorf("Have = %b, want all three", a.Have)
	}
	if !a.Start.Equal(b.Start) {
		t.Errorf("start %v vs %v", a.Start, b.Start)
	}
	if a.Parent == 0 || a.Parent != b.Parent {
		t.Errorf("parent %d vs %d", a.Parent, b.Parent)
	}
	if a.Threads == 0 || b.Threads == 0 {
		t.Errorf("threads %d vs %d", a.Threads, b.Threads)
	}
	within := func(name string, x, y float64, tol float64) {
		if d := x - y; d > tol*(y+1) || -d > tol*(y+1) {
			t.Errorf("%s %v vs %v", name, x, y)
		}
	}
	within("cpu", a.CPUSeconds, b.CPUSeconds, 0.5)
	within("working set", float64(a.WorkingSet), float64(b.WorkingSet), 0.5)
	within("private", float64(a.PrivateBytes), float64(b.PrivateBytes), 0.5)
	within("handles", float64(a.Handles), float64(b.Handles), 0.5)
	// Nothing the old path listed is missing, and the idle pseudo-process is not.
	for _, s := range slow {
		if s.PID == 0 {
			t.Fatal("toolhelp listed pid 0")
		}
	}
	for _, f := range fast {
		if f.PID == 0 {
			t.Fatal("system read listed pid 0")
		}
	}
	if len(fast) < len(slow)-20 {
		t.Errorf("system read lists %d processes, toolhelp %d", len(fast), len(slow))
	}
}

// Two calls close together share one read; a call after the window reads again.
func TestProcessTableReusesARecentRead(t *testing.T) {
	w := newTestWinSource(t)
	t0 := time.Now()
	if _, err := w.table.appendTo(nil, t0); err != nil {
		t.Fatal(err)
	}
	read1 := w.table.at
	if _, err := w.table.appendTo(nil, t0.Add(processReadReuse/2)); err != nil {
		t.Fatal(err)
	}
	if !w.table.at.Equal(read1) {
		t.Error("a call inside the window read the machine again")
	}
	if _, err := w.table.appendTo(nil, t0.Add(processReadReuse+time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if w.table.at.Equal(read1) {
		t.Error("a call after the window was served stale rows")
	}
}

func TestProcessTableNamesAreReused(t *testing.T) {
	w := newTestWinSource(t)
	rows, err := w.table.appendTo(nil, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || len(w.table.names) == 0 {
		t.Fatalf("rows=%d names=%d", len(rows), len(w.table.names))
	}
	if len(w.table.names) > len(rows) {
		t.Errorf("name cache %d larger than %d rows", len(w.table.names), len(rows))
	}
}

func BenchmarkProcessesSystemRead(b *testing.B) {
	w := newTestWinSource(b)
	var dst []ProcSample
	b.ReportAllocs()
	clock := time.Now()
	for i := 0; i < b.N; i++ {
		var err error
		clock = clock.Add(time.Second) // every call is outside the reuse window: a real read
		dst, err = w.table.appendTo(dst[:0], clock)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(dst)), "procs")
}

func BenchmarkProcessesToolhelp(b *testing.B) {
	w := newTestWinSource(b)
	var dst []ProcSample
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var err error
		dst, err = w.processesToolhelp(dst[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(dst)), "procs")
}
