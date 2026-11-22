package sampler

import (
	"testing"

	"api-audit-explorer/internal/leakcheck"
)

// BL-424: a recording started and stopped five times, on the real ticker, leaves
// no goroutine behind and closes its source each time.
func TestSamplerStartStopLeavesNoGoroutines(t *testing.T) {
	log := &importLog{}
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(int) {
		f := newFake(smallServer)
		s, err := Start(Config{IntervalSeconds: 15, Import: log.Import, Source: f})
		if err != nil {
			t.Fatal(err)
		}
		s.Stop()
		<-s.Done()
		if !f.isClosed() {
			t.Fatal("the source was not closed")
		}
	})
}

// TestSamplerClockedRunLeavesNoGoroutines drives a few ticks by hand first.
func TestSamplerClockedRunLeavesNoGoroutines(t *testing.T) {
	log := &importLog{}
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(int) {
		clk := newFakeClock()
		s := startFake(t, newFake(smallServer), clk, log, nil)
		clk.send(t, s, at(15), 1)
		clk.send(t, s, at(30), 2)
		s.Stop()
		<-s.Done()
	})
}
