// Package clock turns the timestamps found in exported files into UTC without
// guessing. Client Windows machines ship no time-zone database, so the
// package embeds one (time/tzdata, about 450 KB).
package clock

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata" // embedded zone database; Windows has none otherwise
)

// Rule names say how a timestamp became UTC. They are stored on every event
// (Event.TZRule) and in the source-quality statement.
const (
	RuleUTCField      = "utc-field"       // the field is UTC by definition (IIS W3C, ISO "Z")
	RuleOffsetInField = "offset-in-field" // the value carries its own offset
	RuleHeaderBias    = "header-bias"     // the file header states a bias (PDH-CSV)
	RuleHostLocal     = "host-local"      // host time zone chosen by the user, DST resolved
)

// Errors never carry the offending value.
var (
	ErrBadTime    = errors.New("clock: unparsable time")
	ErrNoLocation = errors.New("clock: no location")
)

// LocalResult is a wall-clock time resolved in a zone. In a DST fold the
// wall time happens twice (Ambiguous) and in a gap it never happens
// (Nonexistent); UTC then holds a placeholder (the earlier instant for a fold,
// the pre-transition offset for a gap) that the caller must not trust.
type LocalResult struct {
	UTC                    time.Time
	Ambiguous, Nonexistent bool
}

// offsetInLayout reports whether a layout carries a numeric offset ("Z07:00",
// "-0700", "-07"); time.Parse then honours it.
func offsetInLayout(layout string) bool {
	return strings.Contains(layout, "Z07") || strings.Contains(layout, "-07")
}

// ParseLocal parses value with layout as a wall-clock time in loc. A DST fold
// and a DST gap are flagged, never guessed. If the layout carries a numeric
// offset, the value is converted with that offset and neither flag is set.
// A layout with only a zone abbreviation ("MST") is accepted only when the
// value says UTC or GMT: other abbreviations are ambiguous (IST, CST) and
// time.Parse would silently read an unknown one as offset 0, so they are
// ErrBadTime. The error never includes the value.
func ParseLocal(layout, value string, loc *time.Location) (LocalResult, error) {
	if loc == nil {
		return LocalResult{}, ErrNoLocation
	}
	t, err := time.Parse(layout, value)
	if err != nil {
		return LocalResult{}, ErrBadTime
	}
	if offsetInLayout(layout) {
		return LocalResult{UTC: t.UTC()}, nil
	}
	if strings.Contains(layout, "MST") {
		if name, _ := t.Zone(); name == "UTC" || name == "GMT" {
			return LocalResult{UTC: t.UTC()}, nil
		}
		return LocalResult{}, ErrBadTime
	}
	// t holds the wall fields as if they were UTC. A real instant in loc has
	// wall = instant + offset, so candidate instants are wall - offset for the
	// offsets in force on either side of the date.
	wall := t
	var offsets []int
	for _, probe := range []time.Time{wall.Add(-36 * time.Hour), wall.Add(36 * time.Hour)} {
		_, off := probe.In(loc).Zone()
		dup := false
		for _, o := range offsets {
			dup = dup || o == off
		}
		if !dup {
			offsets = append(offsets, off)
		}
	}
	var valid []time.Time
	for _, off := range offsets {
		inst := wall.Add(-time.Duration(off) * time.Second)
		if _, got := inst.In(loc).Zone(); got == off {
			valid = append(valid, inst)
		}
	}
	switch len(valid) {
	case 1:
		return LocalResult{UTC: valid[0].UTC()}, nil
	case 0:
		_, off := wall.Add(-36 * time.Hour).In(loc).Zone()
		return LocalResult{UTC: wall.Add(-time.Duration(off) * time.Second).UTC(), Nonexistent: true}, nil
	default:
		first := valid[0]
		if valid[1].Before(first) {
			first = valid[1]
		}
		return LocalResult{UTC: first.UTC(), Ambiguous: true}, nil
	}
}

// BiasLocation returns the fixed zone for a PDH-style header bias: minutes
// WEST of UTC, so a bias of 480 is UTC-8 and -60 is UTC+1. It cannot follow
// DST; a counter file spanning a transition shows a different bias per row
// only in old tools, so callers state RuleHeaderBias and accept one offset.
func BiasLocation(biasMinutes int) *time.Location {
	return time.FixedZone("bias", -biasMinutes*60)
}
