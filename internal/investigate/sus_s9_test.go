package investigate

import (
	"testing"
	"time"
)

func TestIsFilterReject(t *testing.T) {
	cases := []struct {
		status, sub string
		want        bool
	}{
		{"404", "5", true},
		{"404", "0", false},
		{"404", "13", false},
		{"413", "1", true},
		{"431", "0", true},
		{"500", "19", false},
	}
	for _, c := range cases {
		if got := isFilterReject(IISRow{Status: c.status, SubStatus: c.sub}); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.status, c.sub, got, c.want)
		}
	}
}

// filterInput builds n IIS rows spread over each window, the first rejected of
// them being 404.7 rows.
func filterInput(nInc, rejInc, nRef, rejRef int) Input {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, IISPresent: true, IISSubStatus: true}
	add := func(w Window, n, rej int) {
		step := w.End.Sub(w.Start) / time.Duration(n+1)
		for i := 0; i < n; i++ {
			e := w.Start.Add(time.Duration(i+1) * step)
			r := IISRow{Start: e.Add(-time.Second), End: e, TakenMs: 100, Path: "/x", Status: "200", SubStatus: "0"}
			if i < rej {
				r.Status, r.SubStatus = "404", "7"
			}
			in.IIS = append(in.IIS, r)
		}
	}
	add(ref, nRef, rejRef)
	add(inc, nInc, rejInc)
	return in
}

func TestSigFilterReject(t *testing.T) {
	th := Current()
	t.Run("no IIS", func(t *testing.T) {
		in := filterInput(30, 4, 0, 0)
		in.IISPresent = false
		s := sigFilterReject(in, Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "iis" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("no sub-status", func(t *testing.T) {
		in := filterInput(30, 0, 0, 0)
		in.IISSubStatus = false
		for i := range in.IIS {
			in.IIS[i].SubStatus = ""
		}
		s := sigFilterReject(in, Facts{}, th, nil)
		if s.Value != Unknown || s.Missing != "substatus" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("rejections with a quiet reference", func(t *testing.T) {
		if s := sigFilterReject(filterInput(30, 4, 30, 0), Facts{}, th, nil); s.Value != Yes {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("same rejections in the reference", func(t *testing.T) {
		if s := sigFilterReject(filterInput(30, 4, 30, 4), Facts{}, th, nil); s.Value != No {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("too few rows", func(t *testing.T) {
		if s := sigFilterReject(filterInput(5, 1, 5, 0), Facts{}, th, nil); s.Value != Unknown || s.Missing != "" {
			t.Fatalf("got %+v", s)
		}
	})
}
