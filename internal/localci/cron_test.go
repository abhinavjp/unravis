package localci

import (
	"strings"
	"testing"
	"time"
)

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCronNext(t *testing.T) {
	for _, tc := range []struct{ spec, after, want string }{
		{"17,47 * * * *", "2026-11-25 10:00", "2026-11-25 10:17"},
		{"17,47 * * * *", "2026-11-25 10:17", "2026-11-25 10:47"},
		{"17,47 * * * *", "2026-11-25 10:47", "2026-11-25 11:17"},
		{"47 1 * * *", "2026-11-25 01:47", "2026-11-26 01:47"},
		{"23 2 * * *", "2026-11-25 23:59", "2026-11-26 02:23"},
		{"17 3 * * 1", "2026-11-25 10:00", "2026-11-30 03:17"}, // 2026-11-25 is a Wednesday
		{"*/15 * * * *", "2026-11-25 10:07", "2026-11-25 10:15"},
		{"*/15 * * * *", "2026-11-25 10:45", "2026-11-25 11:00"},
		{"0 0 1 * *", "2026-11-25 10:00", "2026-12-01 00:00"},
		{"0 0 29 2 *", "2026-11-25 10:00", "2028-02-29 00:00"},
		{"0 9-17/4 * * 1-5", "2026-11-27 17:00", "2026-11-30 09:00"}, // Friday 17:00 is a fire time; next is Monday 09:00
		{"30 6 * * mon,fri", "2026-11-25 10:00", "2026-11-27 06:30"},
		{"0 12 * jan *", "2026-11-25 10:00", "2027-01-01 12:00"},
		{"0 0 * * 7", "2026-11-25 10:00", "2026-11-29 00:00"}, // 7 is Sunday
		{"5/20 * * * *", "2026-11-25 10:30", "2026-11-25 10:45"},
		{"0 0 13 * 5", "2026-11-25 10:00", "2026-11-27 00:00"}, // both day fields restricted: the 13th OR a Friday
	} {
		c, err := ParseCron(tc.spec)
		if err != nil {
			t.Errorf("%q: %v", tc.spec, err)
			continue
		}
		if got := c.Next(utc(tc.after)); !got.Equal(utc(tc.want)) {
			t.Errorf("%q after %s: got %s, want %s", tc.spec, tc.after, got.Format("2006-01-02 15:04"), tc.want)
		}
	}
}

func TestCronRejectsWhatItDoesNotUnderstand(t *testing.T) {
	for spec, why := range map[string]string{
		"@daily":        "5 fields",
		"* * * *":       "5 fields",
		"* * * * * *":   "5 fields",
		"60 * * * *":    "outside 0-59",
		"* 24 * * *":    "outside 0-23",
		"* * 0 * *":     "outside 1-31",
		"* * * 13 *":    "outside 1-12",
		"* * * * 8":     "outside 0-7",
		"5-1 * * * *":   "backwards",
		"*/0 * * * *":   "step",
		"*/x * * * *":   "step",
		"? * * * *":     "unsupported",
		"L * * * *":     "unsupported",
		"* * L * *":     "unsupported",
		"* * * * 5#2":   "unsupported",
		"1,,2 * * * *":  "empty item",
		"* * * foo *":   "unsupported",
		"* * * * -1":    "unsupported",
		"1-2-3 * * * *": "unsupported",
		"* * 30W * *":   "unsupported",
		"H * * * *":     "unsupported",
		"1/ * * * *":    "step",
	} {
		_, err := ParseCron(spec)
		if err == nil {
			t.Errorf("%q was accepted", spec)
			continue
		}
		if !strings.Contains(err.Error(), why) {
			t.Errorf("%q: error %q does not mention %q", spec, err, why)
		}
	}
}

func TestCronLastBetween(t *testing.T) {
	c, _ := ParseCron("17,47 * * * *")
	got := c.LastBetween(utc("2026-11-25 08:00"), utc("2026-11-25 10:20"))
	if !got.Equal(utc("2026-11-25 10:17")) {
		t.Fatalf("got %s", got)
	}
	if got := c.LastBetween(utc("2026-11-25 10:17"), utc("2026-11-25 10:30")); !got.IsZero() {
		t.Fatalf("nothing falls in (10:17, 10:30], got %s", got)
	}
}
