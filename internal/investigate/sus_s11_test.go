package investigate

import (
	"testing"
	"time"
)

func recvInput(present bool, reason string, times ...time.Time) Input {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, HTTPErrPresent: present}
	for _, t := range times {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: t, Reason: reason})
	}
	return in
}

func TestSigRecvTimeout(t *testing.T) {
	th := Current()

	t.Run("no log is missing", func(t *testing.T) {
		s := sigRecvTimeout(recvInput(false, "Timer_HeaderWait"), Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "httperr" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("burst with nothing in the reference", func(t *testing.T) {
		in := recvInput(true, "Timer_HeaderWait", at(10, 10), at(10, 11), at(10, 12), at(10, 13))
		s := sigRecvTimeout(in, Facts{}, th, nil)
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("burst but reference rate is higher", func(t *testing.T) {
		in := recvInput(true, "Timer_HeaderWait", at(10, 10), at(10, 11), at(10, 12), at(10, 13))
		for i := 0; i < 8; i++ {
			in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(9, 5+i*6), Reason: "Timer_HeaderWait"})
		}
		s := sigRecvTimeout(in, Facts{}, th, nil)
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("one row is no burst", func(t *testing.T) {
		s := sigRecvTimeout(recvInput(true, "Timer_EntityBody", at(10, 10)), Facts{}, th, nil)
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
}
