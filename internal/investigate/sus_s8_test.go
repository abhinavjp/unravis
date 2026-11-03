package investigate

import (
	"testing"
	"time"
)

func payloadInput(nRef, nInc int, pRef, pInc float64) Input {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, Audit: AuditPresent}
	add := func(w Window, n int, p float64) {
		for i := 0; i < n; i++ {
			v := p
			in.Requests = append(in.Requests, Request{Start: w.Start.Add(time.Duration(i) * time.Minute / 2), P: &v})
		}
	}
	add(ref, nRef, pRef)
	add(inc, nInc, pInc)
	return in
}

func TestSigPayloadUp(t *testing.T) {
	th := Current()
	t.Run("audit missing", func(t *testing.T) {
		in := payloadInput(30, 30, 50, 400)
		in.Audit = AuditMissing
		s := sigPayloadUp(in, Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "audit" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("no P at all", func(t *testing.T) {
		in := payloadInput(30, 30, 50, 400)
		for i := range in.Requests {
			in.Requests[i].P = nil
		}
		s := sigPayloadUp(in, Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "payload" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("too few values", func(t *testing.T) {
		s := sigPayloadUp(payloadInput(10, 10, 50, 400), Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("big rise", func(t *testing.T) {
		if s := sigPayloadUp(payloadInput(30, 30, 50, 400), Facts{}, th, nil); s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("rise under the ms mark", func(t *testing.T) {
		if s := sigPayloadUp(payloadInput(30, 30, 500, 560), Facts{}, th, nil); s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("rise under the percent mark", func(t *testing.T) {
		if s := sigPayloadUp(payloadInput(30, 30, 1000, 1150), Facts{}, th, nil); s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
}

func TestS8OnStepScenario(t *testing.T) {
	r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addA: 400}))
	s := suspect(t, r, "S8")
	t.Logf("S8 = %s/%s: %s", s.Result, s.Colour, s.Why)
	if s.Colour == Red {
		t.Errorf("S8 is Red on an audit set-up step")
	}
	wantSuspect(t, r, "S5", ResConsistent, Red)
}
