//go:build windows

package blackbox

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"
)

// TestRealSoakNoGrowth runs the REAL host collector and every REAL group for many
// ticks and reports goroutines, post-GC heap, handles and private bytes at the
// start, middle and end. Opt-in (UNRAVIS_SOAK=<ticks>) because it reads the live
// system. It fails when goroutines or handles keep growing after warm-up.
func TestRealSoakNoGrowth(t *testing.T) {
	v := os.Getenv("UNRAVIS_SOAK")
	if v == "" {
		t.Skip("set UNRAVIS_SOAK=<ticks> to soak the real collectors")
	}
	ticks, _ := strconv.Atoi(v)
	if ticks < 20 {
		ticks = 20
	}
	col, err := OSCollector(true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := col.Prime(now); err != nil {
		t.Fatal(err)
	}
	groups := BuildGroups(GroupDeps{Cores: CoreCount()})
	defer func() {
		for _, g := range groups {
			if c, ok := g.(interface{ Close() error }); ok {
				_ = c.Close()
			}
		}
	}()
	type snap struct {
		tick                     int
		gor                      int
		heapKB, privKB           uint64
		handles                  uint32
	}
	take := func(i int) snap {
		runtime.GC()
		debug.FreeOSMemory()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		r, _ := SelfUsage()
		return snap{i, runtime.NumGoroutine(), ms.HeapInuse / 1024, r.Private / 1024, r.Handles}
	}
	var snaps []snap
	var prev time.Time
	for i := 0; i < ticks; i++ {
		time.Sleep(250 * time.Millisecond)
		now = time.Now()
		_, _ = col.Collect(now)
		dt := 0.0
		if !prev.IsZero() {
			dt = now.Sub(prev).Seconds()
		}
		prev = now
		for _, g := range groups {
			_, _ = g.Collect(now, dt)
		}
		if i == 9 || i == ticks/2 || i == ticks-1 {
			s := take(i)
			snaps = append(snaps, s)
			t.Logf("tick %3d: goroutines %d, heap %d KiB, private %d KiB, handles %d", s.tick, s.gor, s.heapKB, s.privKB, s.handles)
		}
	}
	a, z := snaps[0], snaps[len(snaps)-1]
	if z.gor > a.gor+3 {
		t.Errorf("goroutines grew %d -> %d", a.gor, z.gor)
	}
	if z.handles > a.handles+20 {
		t.Errorf("handles grew %d -> %d", a.handles, z.handles)
	}
	if z.heapKB > a.heapKB+4096 {
		t.Errorf("heap grew %d -> %d KiB", a.heapKB, z.heapKB)
	}
}
