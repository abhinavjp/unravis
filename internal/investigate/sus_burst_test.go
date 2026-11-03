package investigate

import (
	"testing"
	"time"
)

func burstInput(reasonTimes []time.Time, iis int) Input {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, HTTPErrPresent: true}
	for _, t := range reasonTimes {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: t, Reason: "QueueFull"})
	}
	for i := 0; i < iis; i++ {
		e := inc.Start.Add(time.Duration(i) * 10 * time.Second)
		in.IIS = append(in.IIS, IISRow{Start: e.Add(-time.Second), End: e, TakenMs: 1000, Path: "/x"})
	}
	return in
}

func TestHTTPErrBurst(t *testing.T) {
	th := Current()

	t.Run("no log is missing", func(t *testing.T) {
		in := burstInput(nil, 0)
		in.HTTPErrPresent = false
		s, _ := httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != Unknown || s.Missing != "httperr" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("three in four minutes", func(t *testing.T) {
		in := burstInput([]time.Time{at(10, 10), at(10, 12), at(10, 14)}, 0)
		s, b := httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != Yes || b.Peak5 != 3 {
			t.Fatalf("got %+v peak %d", s, b.Peak5)
		}
	})
	t.Run("two far apart", func(t *testing.T) {
		in := burstInput([]time.Time{at(10, 10), at(10, 30)}, 0)
		s, _ := httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("share of requests", func(t *testing.T) {
		in := burstInput([]time.Time{at(10, 10), at(10, 20), at(10, 30), at(10, 40)}, 300)
		s, b := httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != Yes || b.Peak5 != 1 {
			t.Fatalf("got %+v peak %d", s, b.Peak5)
		}
	})
	t.Run("two below share", func(t *testing.T) {
		in := burstInput([]time.Time{at(10, 10), at(10, 20)}, 300)
		s, _ := httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("rise needed and reference at same rate", func(t *testing.T) {
		// 3 rows in 90 incident minutes; 2 rows in 60 reference minutes: same rate.
		in := burstInput([]time.Time{at(10, 10), at(10, 12), at(10, 14), at(9, 10), at(9, 40)}, 0)
		s, _ := httpErrBurst(in, th, true, "Queue rows", "QueueFull")
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
		s, _ = httpErrBurst(in, th, false, "Queue rows", "QueueFull")
		if s.Value != Yes {
			t.Fatalf("without rise: %+v", s)
		}
	})
	t.Run("reason folds case", func(t *testing.T) {
		in := burstInput([]time.Time{at(10, 10), at(10, 12), at(10, 14)}, 0)
		s, _ := httpErrBurst(in, th, false, "Queue rows", "queuefull")
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
}
