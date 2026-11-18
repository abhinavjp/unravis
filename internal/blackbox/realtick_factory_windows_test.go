//go:build windows

package blackbox

import (
	"os"
	"testing"
	"time"
)

// TestRealFactoryErrors builds every PDH-backed group source on its own and
// reports the real error and the time a failed attempt costs. Opt-in.
func TestRealFactoryErrors(t *testing.T) {
	if os.Getenv("UNRAVIS_REALTICK") == "" {
		t.Skip("set UNRAVIS_REALTICK=1")
	}
	sets := map[string][]string{
		"procIO": procIOPaths(),
		"proc10": proc10Paths,
		"clr":    clrPaths,
		"iis":    iisPaths,
		"aspnet": aspnetPaths,
		"http":   httpPaths,
		"tcp":    tcpRatePaths(),
		"mem":    memPressurePaths,
	}
	for name, paths := range sets {
		for k := 0; k < 2; k++ {
			c1, s1 := cpuSeconds(), time.Now()
			src, err := NewPDHSource(paths)
			t.Logf("%-7s try %d: err=%v wall=%.1fms cpu=%.1fms", name, k, err,
				float64(time.Since(s1).Microseconds())/1000, (cpuSeconds()-c1)*1000)
			if err == nil {
				_ = src.Close()
			}
		}
	}
}
