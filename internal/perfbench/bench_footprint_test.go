package perfbench

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"
)

// TestBenchOpenCaseFootprint is the BL-423 / BL-424 standard run: import a
// synthetic counters case (UNRAVIS_BENCH_COUNTER_SECONDS, default 6 h, 200
// counters at 15 s), then open it the way four charts do (four concurrent series
// requests, three rounds), and report Go heap and resident memory at the peak,
// after the requests, and after a short idle wait; then count the process's
// wakeups over an idle window. Gated by UNRAVIS_BENCH=1. Runs on Linux and
// Windows; wakeups are only counted on Linux.
func TestBenchOpenCaseFootprint(t *testing.T) {
	need(t)
	span := envInt("UNRAVIS_BENCH_COUNTER_SECONDS", 6*3600)
	r := importCase(t, fmt.Sprintf("footprint-import-counters-200x%ds@15s", span), "win-counters", func(p string) (int64, error) {
		b, _, _, err := genPDH(p, span, 15)
		return b, err
	})
	from := time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Duration(span) * time.Second)
	sel := func(k int) string {
		return "kind=counter;source=win-counters;host=web-a;attr.object=Process;attr.counter=% Processor Time;attr.instance=" + fmt.Sprintf("w3wp#%d", k)
	}
	chart := func(first, n int) string {
		q := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}, "bucket": {"300000"}}
		for k := first; k < first+n; k++ {
			q.Add("sel", sel(k))
		}
		return "/api/cases/" + r.caseID + "/series?" + q.Encode()
	}
	paths := []string{chart(0, 10), chart(10, 10), chart(20, 10), chart(30, 10)}

	debug.FreeOSMemory()
	ws0, _, _ := osMem()
	var ms0 runtime.MemStats
	runtime.ReadMemStats(&ms0)

	m := startMeter("")
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		for _, p := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := r.do(http.MethodGet, p, "", nil)
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}()
		}
		wg.Wait()
	}
	meas := m.finish()
	var msA runtime.MemStats
	runtime.ReadMemStats(&msA)
	wsA, _, _ := osMem()

	time.Sleep(35 * time.Second) // past the idle-connection timeout of the case pool
	var msB runtime.MemStats
	runtime.ReadMemStats(&msB)
	wsB, _, _ := osMem()
	debug.FreeOSMemory()
	var msC runtime.MemStats
	runtime.ReadMemStats(&msC)
	wsC, _, _ := osMem()

	idle := envInt("UNRAVIS_BENCH_IDLE_SECONDS", 30)
	w0, ok := wakeups()
	c0u, c0k := osCPU()
	time.Sleep(time.Duration(idle) * time.Second)
	w1, _ := wakeups()
	c1u, c1k := osCPU()
	perMin := float64(w1-w0) * 60 / float64(idle)
	cpuMs := float64((c1u+c1k)-(c0u+c0k)) / float64(time.Millisecond) * 60 / float64(idle)

	// The first request after the idle wait pays for reconnecting; the second is warm.
	timed := func() time.Duration {
		t0 := time.Now()
		res := r.do(http.MethodGet, paths[0], "", nil)
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return time.Since(t0)
	}
	cold, warm := timed(), timed()

	line := fmt.Sprintf("FOOTPRINT before: heapInuse=%sMiB rss=%sMiB | requests: peak-heapInuse=%sMiB peak-heapSys=%sMiB peak-rss=%sMiB hwm=%sMiB | after requests: heapInuse=%sMiB rss=%sMiB | after 35 s idle: heapInuse=%sMiB rss=%sMiB | after FreeOSMemory: heapInuse=%sMiB rss=%sMiB | idle: wakeups/min=%.0f (counted=%t) cpu-ms/min=%.1f goroutines=%d | request-phase wall=%.2fs cold-after-idle=%s warm=%s",
		mib(ms0.HeapInuse), mib(ws0), mib(meas.MaxHeapInuse), mib(meas.MaxSys), mib(meas.MaxWS), mib(meas.PeakWS),
		mib(msA.HeapInuse), mib(wsA), mib(msB.HeapInuse), mib(wsB), mib(msC.HeapInuse), mib(wsC), perMin, ok, cpuMs, runtime.NumGoroutine(), meas.Wall.Seconds(), cold.Round(time.Millisecond), warm.Round(time.Millisecond))
	t.Log(line)
	if out := os.Getenv("UNRAVIS_BENCH_OUT"); out != "" {
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, line)
			_ = f.Close()
		}
	}
}
