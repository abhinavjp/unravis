//go:build windows

package blackbox

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestRealMustHaveCounters reads the new machine counters from the REAL sources
// and logs what comes back, so a wrong English path shows up as a missing field.
// Opt-in (UNRAVIS_REALTICK=1): it reads the live system.
func TestRealMustHaveCounters(t *testing.T) {
	if os.Getenv("UNRAVIS_REALTICK") == "" {
		t.Skip("set UNRAVIS_REALTICK=1 to read the real counters")
	}
	var gs []Group
	for _, name := range []string{"memPressure", "diskCpu", "tcp"} {
		registryMu.Lock()
		reg := registry[name]
		registryMu.Unlock()
		g, err := reg.factory(GroupDeps{Cores: CoreCount()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c, ok := g.(interface{ Close() error }); ok {
			defer c.Close()
		}
		gs = append(gs, g)
	}
	now := time.Now()
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		now = time.Now()
		for _, g := range gs {
			t0 := time.Now()
			v, err := g.Collect(now, 2)
			if i == 4 {
				b, _ := json.Marshal(v)
				t.Logf("%s (%v) err=%v %s", g.Name(), time.Since(t0).Round(time.Microsecond), err, b)
			}
		}
	}
}
