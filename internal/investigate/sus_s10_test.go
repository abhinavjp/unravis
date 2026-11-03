package investigate

import (
	"testing"
	"time"
)

func poolInput(present bool, rows ...HTTPErrRow) Input {
	ref, inc := stdWindows()
	return Input{Incident: inc, Reference: ref, HTTPErrPresent: present, HTTPErr: rows}
}

func TestSigAppPoolFail(t *testing.T) {
	th := Current()
	row := func(tm time.Time, reason string) HTTPErrRow { return HTTPErrRow{T: tm, Reason: reason} }

	t.Run("no log is missing", func(t *testing.T) {
		s := sigAppPoolFail(poolInput(false), Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "httperr" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("log with no rows", func(t *testing.T) {
		s := sigAppPoolFail(poolInput(true), Facts{}, th, nil)
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("one AppOffline row in the incident", func(t *testing.T) {
		s := sigAppPoolFail(poolInput(true, row(at(10, 20), "AppOffline")), Facts{}, th, nil)
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("reason folds case", func(t *testing.T) {
		s := sigAppPoolFail(poolInput(true, row(at(10, 20), "connection_abandoned_by_apppool")), Facts{}, th, nil)
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("row only in the reference", func(t *testing.T) {
		s := sigAppPoolFail(poolInput(true, row(at(9, 30), "AppShutdown")), Facts{}, th, nil)
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
}
