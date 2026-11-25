package localci

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed `on.schedule` entry: the five-field subset of cron that GitHub Actions accepts and the
// workflows use. Each field is a list of `*`, a number, a range `a-b`, and an optional step `/n` (`*/15`,
// `1-5/2`), months and weekdays may also be written as three-letter names. Times are UTC, as on GitHub.
// Anything else (@daily, ?, L, W, #, six fields) is rejected: a schedule that cannot be understood must not
// be skipped quietly.
type Cron struct {
	Spec                       string
	min, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar           bool   // the field started with `*`: then both day fields must match, else either one
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dowNames   = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
)

// ParseCron parses one schedule entry.
func ParseCron(spec string) (*Cron, error) {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week), got %d", spec, len(f))
	}
	c := &Cron{Spec: strings.Join(f, " ")}
	var err error
	if c.min, err = cronField(f[0], 0, 59, nil, spec, "minute"); err != nil {
		return nil, err
	}
	if c.hour, err = cronField(f[1], 0, 23, nil, spec, "hour"); err != nil {
		return nil, err
	}
	if c.dom, err = cronField(f[2], 1, 31, nil, spec, "day-of-month"); err != nil {
		return nil, err
	}
	if c.month, err = cronField(f[3], 1, 12, monthNames, spec, "month"); err != nil {
		return nil, err
	}
	if c.dow, err = cronField(f[4], 0, 7, dowNames, spec, "day-of-week"); err != nil {
		return nil, err
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow = c.dow&^(1<<7) | 1
	}
	c.domStar, c.dowStar = strings.HasPrefix(f[2], "*"), strings.HasPrefix(f[4], "*")
	return c, nil
}

func cronField(s string, lo, hi int, names map[string]int, spec, what string) (uint64, error) {
	bad := func(why string) error { return fmt.Errorf("cron %q: %s field %q: %s", spec, what, s, why) }
	num := func(t string) (int, error) {
		if v, ok := names[strings.ToLower(t)]; ok {
			return v, nil
		}
		v, err := strconv.Atoi(t)
		if err != nil || strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-") {
			return 0, bad(fmt.Sprintf("%q is not a number (unsupported syntax)", t))
		}
		if v < lo || v > hi {
			return 0, bad(fmt.Sprintf("%d is outside %d-%d", v, lo, hi))
		}
		return v, nil
	}
	var set uint64
	for _, part := range strings.Split(s, ",") {
		if part == "" {
			return 0, bad("empty item")
		}
		step := 1
		body := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return 0, bad("the step after / must be a positive number")
			}
			step, body = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case body == "*":
		case strings.Contains(body, "-"):
			ab := strings.SplitN(body, "-", 2)
			var err error
			if a, err = num(ab[0]); err != nil {
				return 0, err
			}
			if b, err = num(ab[1]); err != nil {
				return 0, err
			}
			if a > b {
				return 0, bad("the range runs backwards")
			}
		default:
			v, err := num(body)
			if err != nil {
				return 0, err
			}
			a, b = v, v
			if strings.Contains(part, "/") {
				b = hi // "5/15" means from 5 to the end in steps of 15
			}
		}
		for v := a; v <= b; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func (c *Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar || c.dowStar {
		return dom && dow
	}
	return dom || dow // both restricted: either one, as in standard cron
}

// Next is the first fire time after t (UTC, whole minutes). It is the zero time when none exists within eight years.
func (c *Cron) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(8, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
		case c.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
		case c.min&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// LastBetween is the latest fire time in (after, upTo], or the zero time when none fell in that window.
func (c *Cron) LastBetween(after, upTo time.Time) time.Time {
	var last time.Time
	t := after
	for i := 0; i < 20000; i++ {
		n := c.Next(t)
		if n.IsZero() || n.After(upTo) {
			break
		}
		last, t = n, n
	}
	return last
}
