package investigate

import "testing"

func TestSigDrain(t *testing.T) {
	th := Current()
	ref, inc := stdWindows()
	base := func(present bool, reason string, mins ...int) Input {
		in := Input{Incident: inc, Reference: ref, HTTPErrPresent: present}
		for _, m := range mins {
			in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(10, m), Reason: reason})
		}
		return in
	}
	fl := func(o, p Signal) map[string]Signal {
		return map[string]Signal{SigOLarge: o, SigP95Up: p}
	}

	t.Run("outside time rose, app did not", func(t *testing.T) {
		s := sigDrain(base(false, ""), Facts{}, th, fl(sig(Yes, "x"), sig(No, "y")))
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("outside time and app both rose", func(t *testing.T) {
		s := sigDrain(base(false, ""), Facts{}, th, fl(sig(Yes, "x"), sig(Yes, "y")))
		if s.Value != Unknown || s.Missing != "httperr" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("o_large missing carries its source", func(t *testing.T) {
		s := sigDrain(base(false, ""), Facts{}, th, fl(missing("iis", "z"), sig(No, "y")))
		if s.Value != Unknown || s.Missing != "iis" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("dropped connections burst", func(t *testing.T) {
		s := sigDrain(base(true, "Connection_Dropped", 10, 12, 13), Facts{}, th, fl(sig(No, "x"), sig(No, "y")))
		if s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("quiet log and o_large No", func(t *testing.T) {
		s := sigDrain(base(true, ""), Facts{}, th, fl(sig(No, "x"), sig(No, "y")))
		if s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("quiet log, both rose", func(t *testing.T) {
		s := sigDrain(base(true, ""), Facts{}, th, fl(sig(Yes, "x"), sig(Yes, "y")))
		if s.Value != Unknown || s.Missing != "" {
			t.Fatalf("got %+v", s)
		}
	})
}
