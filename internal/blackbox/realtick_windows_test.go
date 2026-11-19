//go:build windows

package blackbox

import (
	"os"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"
)

// cpuSeconds is this process's user+kernel CPU time (GetProcessTimes).
func cpuSeconds() float64 {
	var c, e, k, u syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	if err := syscall.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	f := func(t syscall.Filetime) float64 {
		return float64(uint64(t.HighDateTime)<<32|uint64(t.LowDateTime)) / 1e7
	}
	return f(k) + f(u)
}

// TestRealTickCost times the REAL host collector and every REAL registered group
// on this machine, per tick. It is opt-in (UNRAVIS_REALTICK=1) because it reads
// the live system; the fake-source gate in budget_bench_test.go cannot see the
// cost of PDH, Win32 or process-table calls.
func TestRealTickCost(t *testing.T) {
	if os.Getenv("UNRAVIS_REALTICK") == "" {
		t.Skip("set UNRAVIS_REALTICK=1 to time the real collectors")
	}
	col, err := OSCollector(true)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	if err := col.Prime(t0); err != nil {
		t.Fatal(err)
	}
	groups := BuildGroups(GroupDeps{Cores: CoreCount()})
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name())
	}
	t.Logf("groups built: %v", names)

	type acc struct {
		wall time.Duration
		cpu  float64
		n    int
		max  time.Duration
	}
	stats := map[string]*acc{}
	add := func(name string, w time.Duration, c float64) {
		a := stats[name]
		if a == nil {
			a = &acc{}
			stats[name] = a
		}
		a.wall += w
		a.cpu += c
		a.n++
		if w > a.max {
			a.max = w
		}
	}
	now := t0
	var prev time.Time
	for i := 0; i < 8; i++ {
		time.Sleep(2 * time.Second)
		now = time.Now()
		c0, s0 := cpuSeconds(), time.Now()
		_, err := col.Collect(now)
		if err != nil {
			t.Logf("host collect: %v", err)
		}
		if i > 0 {
			add("HOST", time.Since(s0), cpuSeconds()-c0)
		}
		dt := 0.0
		if !prev.IsZero() {
			dt = now.Sub(prev).Seconds()
		}
		prev = now
		for _, g := range groups {
			c1, s1 := cpuSeconds(), time.Now()
			_, _ = g.Collect(now, dt)
			if i > 1 {
				add(g.Name(), time.Since(s1), cpuSeconds()-c1)
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var totWall time.Duration
	var totCPU float64
	for _, k := range keys {
		a := stats[k]
		t.Logf("%-12s avg wall %8.1f ms  max %8.1f ms  avg cpu %8.1f ms  (n=%d)", k,
			float64(a.wall.Milliseconds())/float64(a.n), float64(a.max.Microseconds())/1000, a.cpu*1000/float64(a.n), a.n)
		totWall += a.wall / time.Duration(a.n)
		totCPU += a.cpu / float64(a.n)
	}
	t.Logf("TOTAL per tick: wall %.1f ms, cpu %.1f ms; heapInUse %.1f MB, sys %.1f MB",
		float64(totWall.Microseconds())/1000, totCPU*1000, float64(ms.HeapInuse)/1048576, float64(ms.Sys)/1048576)
}
