package investigate

import (
	"testing"
	"time"
)

func TestSpreadOK(t *testing.T) {
	th := Current()
	rows := []struct {
		name   string
		counts []int
		want   bool
	}{
		{"nil", nil, false},
		{"all zero is skipped", []int{0, 0, 0, 0}, true},
		{"trickle is skipped", []int{1, 0, 1, 0}, true},
		{"even", []int{3, 3, 3, 3}, true},
		{"one burst", []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 20}, false},
		{"half the minutes hold events", []int{0, 0, 0, 0, 10, 10, 10, 10}, true},
		{"low share but no minute above a quarter", []int{5, 5, 5, 5, 0, 0, 0, 0, 0, 0}, true},
		{"mean exactly one is not skipped", []int{4, 0, 0, 0}, false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if got := spreadOK(r.counts, th); got != r.want {
				t.Errorf("spreadOK(%v) got %v, want %v", r.counts, got, r.want)
			}
		})
	}
}

// even spreads n events over 10 minutes (n/10 each, the remainder in the first).
func even(n int) []int {
	out := make([]int, 10)
	for i := range out {
		out[i] = n / 10
	}
	out[0] += n % 10
	return out
}

func TestEvaluateGate(t *testing.T) {
	th := Current()
	rows := []struct {
		name                 string
		ref, inc             int
		counts               []int
		rate, p95, spreadOne bool
	}{
		{"ref 59", 59, 20, even(59), false, false, true},
		{"ref 60", 60, 20, even(60), true, false, true},
		{"ref 100", 100, 20, even(100), true, true, true},
		{"inc 19", 100, 19, even(100), false, false, true},
		{"one burst", 200, 50, []int{200, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false, false, false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			g := evaluateGate(r.ref, r.inc, r.counts, th)
			if g.RateOK != r.rate || g.P95OK != r.p95 || g.SpreadOK != r.spreadOne {
				t.Errorf("got rate=%v p95=%v spread=%v, want rate=%v p95=%v spread=%v", g.RateOK, g.P95OK, g.SpreadOK, r.rate, r.p95, r.spreadOne)
			}
			if g.RefEvents != r.ref || g.IncEvents != r.inc {
				t.Errorf("events carried wrong: %d/%d", g.RefEvents, g.IncEvents)
			}
		})
	}
}

// gateInput builds the standard scenario with perMin 3 and keeps exactly n
// reference requests, chosen at an even stride so the minutes stay spread.
func gateInput(n int) Input {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 1, ref: ref, inc: inc, perMin: 3, iis: true})
	var refIdx []int
	for i := range in.Requests {
		if ref.Contains(in.Requests[i].Start) {
			refIdx = append(refIdx, i)
		}
	}
	keep := map[int]bool{}
	for k := 0; k < n; k++ {
		keep[refIdx[k*len(refIdx)/n]] = true
	}
	var out []Request
	for i, r := range in.Requests {
		if ref.Contains(r.Start) && !keep[i] {
			continue
		}
		out = append(out, r)
	}
	in.Requests = out
	return in
}

func TestGateTooFewReferenceEvents(t *testing.T) {
	f := ComputeFacts(gateInput(59), Current())
	if f.Ref.N != 59 || f.Gate.RateOK || f.Ref.PerSec != nil || f.Incident.PerSec != nil || f.Load.LambdaDx != "unknown" {
		t.Errorf("ref N %d gate %+v refPerSec %v lambda %q", f.Ref.N, f.Gate, f.Ref.PerSec, f.Load.LambdaDx)
	}
}

func TestGateExactlyEnoughReferenceEvents(t *testing.T) {
	f := ComputeFacts(gateInput(60), Current())
	if f.Ref.N != 60 || !f.Gate.RateOK || f.Ref.PerSec == nil {
		t.Errorf("ref N %d gate %+v refPerSec %v", f.Ref.N, f.Gate, f.Ref.PerSec)
	}
}

func TestGateP95Boundary(t *testing.T) {
	f := ComputeFacts(gateInput(99), Current())
	if f.Ref.N != 99 || f.Gate.P95OK || f.Ref.P95 != nil {
		t.Errorf("99: ref N %d gate %+v P95 %v", f.Ref.N, f.Gate, f.Ref.P95)
	}
	f = ComputeFacts(gateInput(100), Current())
	if f.Ref.N != 100 || !f.Gate.P95OK || f.Ref.P95 == nil {
		t.Errorf("100: ref N %d gate %+v P95 %v", f.Ref.N, f.Gate, f.Ref.P95)
	}
}

func TestGateSpikyReferenceIsGrey(t *testing.T) {
	in := gateInput(180)
	ref, _ := stdWindows()
	i := 0
	for k := range in.Requests {
		r := &in.Requests[k]
		if !ref.Contains(r.Start) {
			continue
		}
		d := r.End.Sub(r.Start)
		r.Start = at(9, 30).Add(time.Duration(i%50) * time.Second)
		e := r.Start.Add(d)
		r.End = &e
		i++
	}
	f := ComputeFacts(in, Current())
	if f.Gate.SpreadOK || f.Gate.RateOK || f.Ref.PerSec != nil {
		t.Errorf("gate %+v refPerSec %v", f.Gate, f.Ref.PerSec)
	}
}

func TestGateDoesNotChangeASteadyRun(t *testing.T) {
	f := ComputeFacts(stepScenario(mod{}), Current())
	if !f.Gate.RateOK || f.Ref.PerSec == nil {
		t.Errorf("gate %+v refPerSec %v", f.Gate, f.Ref.PerSec)
	}
}
