//go:build windows

package blackbox

import (
	"os"
	"testing"
	"time"
)

// TestRealTargetGroupCost times the target-driven groups (procIO, proc10, clr,
// iis) on this machine with one real process as the target, once with the
// process set unchanged and once with it forced "changed" (a wildcard
// re-expansion every tick). Opt-in (UNRAVIS_REALTICK=1).
func TestRealTargetGroupCost(t *testing.T) {
	if os.Getenv("UNRAVIS_REALTICK") == "" {
		t.Skip("set UNRAVIS_REALTICK=1 to time the real collectors")
	}
	col, err := OSCollector(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := col.Prime(time.Now()); err != nil {
		t.Fatal(err)
	}
	procs, err := col.SnapshotProcesses()
	if err != nil {
		t.Fatal(err)
	}
	var target ResolvedTarget
	for _, p := range procs {
		if p.Name == "node.exe" && p.StartTicks != 0 {
			target = ResolvedTarget{Key: "svc:probe", PID: p.PID, Start: p.Start, StartTicks: p.StartTicks, Runtime: 2}
			break
		}
	}
	if target.PID == 0 {
		t.Skip("no node.exe to use as a target")
	}
	t.Logf("%d processes on this machine, target pid %d", len(procs), target.PID)
	for _, forceChanged := range []bool{false, true} {
		changed := forceChanged
		deps := GroupDeps{Cores: CoreCount(),
			Targets:           func() []ResolvedTarget { return []ResolvedTarget{target} },
			ProcessSetChanged: func() bool { return changed }}
		groups := BuildGroups(deps)
		type acc struct {
			wall time.Duration
			cpu  float64
			n    int
		}
		stats := map[string]*acc{}
		var prev time.Time
		for i := 0; i < 7; i++ {
			time.Sleep(2 * time.Second)
			now := time.Now()
			dt := 0.0
			if !prev.IsZero() {
				dt = now.Sub(prev).Seconds()
			}
			prev = now
			for _, g := range groups {
				switch g.Name() {
				case "procIO", "proc10", "clr", "iis", "aspnet", "sqlHost", "http":
				default:
					continue
				}
				c1, s1 := cpuSeconds(), time.Now()
				v, gerr := g.Collect(now, dt)
				if i == 3 {
					t.Logf("  %-8s %T err=%v", g.Name(), g, gerr)
					_ = v
				}
				if i > 1 {
					a := stats[g.Name()]
					if a == nil {
						a = &acc{}
						stats[g.Name()] = a
					}
					a.wall += time.Since(s1)
					a.cpu += cpuSeconds() - c1
					a.n++
				}
			}
		}
		var totW time.Duration
		var totC float64
		for name, a := range stats {
			t.Logf("changed=%v %-8s avg wall %7.1f ms  avg cpu %7.1f ms", forceChanged, name,
				float64(a.wall.Microseconds())/1000/float64(a.n), a.cpu*1000/float64(a.n))
			totW += a.wall / time.Duration(a.n)
			totC += a.cpu / float64(a.n)
		}
		t.Logf("changed=%v TOTAL wall %.1f ms cpu %.1f ms", forceChanged, float64(totW.Microseconds())/1000, totC*1000)
		for _, g := range groups {
			if c, ok := g.(interface{ Close() error }); ok {
				_ = c.Close()
			}
		}
	}
}
