package wincounters

import (
	"strings"
	"testing"
	"time"
)

// The W3SVC_W3WP instance is "<pid>_<pool name>"; only the pid is kept.
func TestW3wpInstanceKeepsPidOnly(t *testing.T) {
	tz := time.FixedZone("synthetic", 13*3600) // matches bias -780
	evs, rep, err := parse(t, fixture(t, "counters_w3wp_a1.csv"), opts(8, tz))
	if err != nil {
		t.Fatal(err)
	}
	var active int
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, "SENTINEL-7f3a") || strings.Contains(v, "Pool") {
				t.Errorf("attr %s=%q holds the pool name", k, v)
			}
		}
		if e.Attrs["object"] == "W3SVC_W3WP" && e.Attrs["counter"] == "Active Requests" {
			active++
			if e.Attrs["instance"] != "pid-1240" {
				t.Errorf("instance %q, want pid-1240", e.Attrs["instance"])
			}
		}
	}
	if active != 3 {
		t.Errorf("%d Active Requests events, want 3", active)
	}
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, "SENTINEL-7f3a") {
			t.Errorf("gap holds the pool name: %q", g)
		}
	}
	uniqueEventKeys(t, evs)
}

func TestKeptInstanceW3wp(t *testing.T) {
	for _, c := range []struct {
		obj, in, want string
		keep          bool
	}{
		{"W3SVC_W3WP", "1240_DefaultAppPool", "pid-1240", true},
		{"w3svc_w3wp", "7_x", "pid-7", true},
		{"W3SVC_W3WP", "_Total", "_Total", true},
		{"W3SVC_W3WP", "DefaultAppPool", "", false},
		{"W3SVC_W3WP", "12345678_pool", "", false}, // more than 7 digits is not a pid
		{"W3SVC_W3WP", "_pool", "", false},
		{"Process", "w3wp", "w3wp", true},
		{"Web Service", "Payroll Site", "", false},
	} {
		got, keep := keptInstance(c.obj, c.in)
		if keep != c.keep || (keep && got != c.want) {
			t.Errorf("keptInstance(%q, %q) = %q, %v; want %q, %v", c.obj, c.in, got, keep, c.want, c.keep)
		}
	}
}
