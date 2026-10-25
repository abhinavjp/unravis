package perfbench

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

// TestBenchSampler runs the sampler's real operating-system source for a minute
// at a 5 s interval and reports this process's CPU and working set. It first
// measures 10 s of the idle test process, so the meter's own cost can be taken
// off. The Windows source only exists on Windows; elsewhere it is skipped.
func TestBenchSampler(t *testing.T) {
	need(t)
	secs := envInt("UNRAVIS_BENCH_SAMPLER_SECONDS", 60)
	interval := envInt("UNRAVIS_BENCH_SAMPLER_INTERVAL", 5)

	idle := startMeterEvery("", time.Second)
	time.Sleep(10 * time.Second)
	idleM := idle.finish()

	var mu sync.Mutex
	var batches, bytes int
	cfg := sampler.Config{
		IntervalSeconds: interval,
		FlushEvery:      20 * time.Second,
		Import: func(name string, body []byte) error {
			mu.Lock()
			batches++
			bytes += len(body)
			mu.Unlock()
			return nil
		},
		Pseudonymize: func(kind, value string) string {
			h := fnv.New32a()
			h.Write([]byte(value))
			return fmt.Sprintf("%s-%08x", kind, h.Sum32())
		},
	}
	m := startMeterEvery("", time.Second)
	s, err := sampler.Start(cfg)
	if err != nil {
		m.finish()
		if errors.Is(err, sampler.ErrNotSupportedOS) {
			t.Skip("the win-live source exists on Windows only")
		}
		t.Fatalf("start: %v", err)
	}
	time.Sleep(time.Duration(secs) * time.Second)
	s.Stop()
	<-s.Done()
	meas := m.finish()
	st := s.Status()
	mu.Lock()
	defer mu.Unlock()

	cpu := meas.CPUUser + meas.CPUKernel
	idleCPU := idleM.CPUUser + idleM.CPUKernel
	pct := func(d, wall time.Duration) float64 { return 100 * d.Seconds() / wall.Seconds() }
	extra := fmt.Sprintf("interval=%ds samples=%d events=%d partial-procs=%d stop=%q batches=%d csv-bytes=%d cpu-of-one-core=%.3f%% (idle test process %.3f%%, net %.3f%%) cpu-of-machine(32 logical)=%.4f%% ws-start=%sMiB ws-max=%sMiB",
		interval, st.Samples, st.Events, st.Partial, st.Stop, batches, bytes,
		pct(cpu, meas.Wall), pct(idleCPU, idleM.Wall), pct(cpu, meas.Wall)-pct(idleCPU, idleM.Wall),
		pct(cpu, meas.Wall)/32, mib(meas.BaseWS), mib(meas.MaxWS))
	report(t, fmt.Sprintf("win-live sampler real source %ds@%ds", secs, interval), meas, extra)
	if st.Samples == 0 {
		t.Errorf("no samples were taken")
	}
}
