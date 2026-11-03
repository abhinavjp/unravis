package investigate

import (
	"testing"
	"time"
)

func queueInput(reason string, ts ...time.Time) Input {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, HTTPErrPresent: true}
	for _, t := range ts {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: t, Reason: reason})
	}
	return in
}

func TestSigQueueBurst(t *testing.T) {
	th := Current()
	t.Run("no HTTPERR log is missing", func(t *testing.T) {
		in := queueInput("QueueFull")
		in.HTTPErrPresent = false
		s := sigQueueBurst(in, Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "httperr" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("three QueueFull in four minutes", func(t *testing.T) {
		in := queueInput("QueueFull", at(10, 10), at(10, 12), at(10, 14))
		if s := sigQueueBurst(in, Facts{}, th, nil); s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("one QueueFull", func(t *testing.T) {
		in := queueInput("QueueFull", at(10, 10))
		if s := sigQueueBurst(in, Facts{}, th, nil); s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("header wait is not a queue reason", func(t *testing.T) {
		in := queueInput("Timer_HeaderWait", at(10, 10), at(10, 12), at(10, 14))
		if s := sigQueueBurst(in, Facts{}, th, nil); s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
}

func TestS1KeepsResultWithoutHTTPErr(t *testing.T) {
	r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400}))
	wantSuspect(t, r, "S1", ResConsistent, Red)

	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400})
	in.HTTPErrPresent = true
	r2 := Analyze(in)
	wantSuspect(t, r2, "S1", ResPartly, Orange)
}
