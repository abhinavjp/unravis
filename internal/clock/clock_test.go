package clock

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const layout = "2006-01-02 15:04:05"

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", name, err)
	}
	return loc
}

func TestParseLocal(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	lord := mustLoc(t, "Australia/Lord_Howe") // 30-minute DST
	cases := []struct {
		name, value string
		loc         *time.Location
		wantUTC     string
		amb, non    bool
	}{
		{"winter", "2001-01-15 12:00:00", ny, "2001-01-15T17:00:00Z", false, false},
		{"summer", "2001-07-15 12:00:00", ny, "2001-07-15T16:00:00Z", false, false},
		{"fold", "2001-10-28 01:30:00", ny, "2001-10-28T05:30:00Z", true, false},
		{"just before fold", "2001-10-28 00:59:59", ny, "2001-10-28T04:59:59Z", false, false},
		{"after fold", "2001-10-28 02:00:00", ny, "2001-10-28T07:00:00Z", false, false},
		{"gap", "2001-04-01 02:30:00", ny, "2001-04-01T07:30:00Z", false, true},
		{"before gap", "2001-04-01 01:59:59", ny, "2001-04-01T06:59:59Z", false, false},
		{"after gap", "2001-04-01 03:00:00", ny, "2001-04-01T07:00:00Z", false, false},
		{"utc", "2001-10-28 01:30:00", time.UTC, "2001-10-28T01:30:00Z", false, false},
		{"lord howe fold", "2001-03-25 01:45:00", lord, "2001-03-24T14:45:00Z", true, false},
		{"lord howe gap", "2001-10-28 02:15:00", lord, "2001-10-27T15:45:00Z", false, true},
	}
	for _, c := range cases {
		got, err := ParseLocal(layout, c.value, c.loc)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if g := got.UTC.Format(time.RFC3339); g != c.wantUTC || got.Ambiguous != c.amb || got.Nonexistent != c.non {
			t.Errorf("%s: got %s amb=%v non=%v, want %s amb=%v non=%v", c.name, g, got.Ambiguous, got.Nonexistent, c.wantUTC, c.amb, c.non)
		}
		if got.UTC.Location() != time.UTC {
			t.Errorf("%s: result not in UTC", c.name)
		}
	}
}

func TestParseLocalZoneInLayout(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	got, err := ParseLocal("2006-01-02T15:04:05Z07:00", "2001-10-28T01:30:00-04:00", ny)
	if err != nil || got.Ambiguous || got.Nonexistent || got.UTC.Format(time.RFC3339) != "2001-10-28T05:30:00Z" {
		t.Errorf("offset layout: %+v %v", got, err)
	}
}

func TestParseLocalErrors(t *testing.T) {
	_, err := ParseLocal(layout, "SENTINEL-7f3a", time.UTC)
	if !errors.Is(err, ErrBadTime) || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("bad value: %v", err)
	}
	if _, err := ParseLocal(layout, "2001-01-01 00:00:00", nil); !errors.Is(err, ErrNoLocation) {
		t.Errorf("nil loc: %v", err)
	}
	if _, err := ParseLocal(layout, "", time.UTC); !errors.Is(err, ErrBadTime) {
		t.Errorf("empty: %v", err)
	}
}

func TestBiasLocation(t *testing.T) {
	got, _ := ParseLocal(layout, "2001-01-01 00:00:00", BiasLocation(480))
	if got.UTC.Format(time.RFC3339) != "2001-01-01T08:00:00Z" {
		t.Errorf("bias 480: %v", got.UTC)
	}
	got, _ = ParseLocal(layout, "2001-01-01 00:00:00", BiasLocation(-60))
	if got.UTC.Format(time.RFC3339) != "2000-12-31T23:00:00Z" {
		t.Errorf("bias -60: %v", got.UTC)
	}
}

func TestWindowsZoneTableLoads(t *testing.T) {
	if len(windowsZones) < 100 {
		t.Fatalf("table has %d entries", len(windowsZones))
	}
	for id, iana := range windowsZones {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("%s -> %s: %v", id, iana, err)
		}
		if _, ok := WindowsZone(id); !ok {
			t.Errorf("WindowsZone(%q) not found", id)
		}
	}
}

func TestWindowsZone(t *testing.T) {
	loc, ok := WindowsZone(" pacific standard time ")
	if !ok || loc.String() != "America/Los_Angeles" {
		t.Fatalf("got %v %v", loc, ok)
	}
	if loc2, _ := WindowsZone("Pacific Standard Time"); loc2 != loc {
		t.Error("lookup not cached")
	}
	if l, ok := WindowsZone("Not A Real Zone"); ok || l != nil {
		t.Error("unmapped id resolved")
	}
	if _, ok := WindowsZone(""); ok {
		t.Error("empty id resolved")
	}
}
