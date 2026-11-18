//go:build windows

package blackbox

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// TestRealMemSplit shows where the recorder's private memory comes from: bare
// process, host collector, then the groups. Opt-in (UNRAVIS_MEMSPLIT=1).
func TestRealMemSplit(t *testing.T) {
	if os.Getenv("UNRAVIS_MEMSPLIT") == "" {
		t.Skip("set UNRAVIS_MEMSPLIT=1")
	}
	show := func(label string) {
		runtime.GC()
		debug.FreeOSMemory()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		r, _ := SelfUsage()
		t.Logf("%-28s private %6d KiB  goHeap %5d KiB  goSys %6d KiB  handles %d", label, r.Private/1024, ms.HeapInuse/1024, ms.Sys/1024, r.Handles)
	}
	show("bare test process")
	col, err := OSCollector(true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = col.Prime(now)
	show("host collector primed")
	for i := 0; i < 3; i++ {
		time.Sleep(300 * time.Millisecond)
		_, _ = col.Collect(time.Now())
	}
	show("host collected x3")
	groups := BuildGroups(GroupDeps{Cores: CoreCount()})
	show("groups built")
	prev := time.Now()
	for i := 0; i < 4; i++ {
		time.Sleep(300 * time.Millisecond)
		n := time.Now()
		for _, g := range groups {
			_, _ = g.Collect(n, n.Sub(prev).Seconds())
		}
		prev = n
	}
	show("groups collected x4")
}
