package schedule

import (
	"time"
)

// civilRange returns the civil dates (as UTC midnights) that can hold a wall
// time inside the window in loc, with a one-day margin on each side.
func (r *run) civilRange(loc *time.Location) (lo, hi time.Time) {
	f, t := r.from.In(loc), r.to.In(loc)
	lo = time.Date(f.Year(), f.Month(), f.Day()-1, 0, 0, 0, 0, time.UTC)
	hi = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	return lo, hi
}

// boundaryAt resolves one scheduled wall time and emits a schedule-boundary
// event when it falls inside the window. A time in a DST gap or fold is
// emitted with a dst flag and a warning severity; it is never guessed.
func (r *run) boundaryAt(line int64, ord *int, w civilWall, loc *time.Location, rule string, base map[string]string) error {
	naive := w.naiveUTC()
	// Offsets never exceed 27 hours from UTC for any zone, so skip cheaply.
	if naive.Before(r.from.Add(-27*time.Hour)) || naive.After(r.to.Add(27*time.Hour)) {
		return nil
	}
	utc, flag := resolveWall(w, loc)
	if !r.inWindow(utc) {
		return nil
	}
	if err := r.countBoundary(); err != nil {
		return err
	}
	a := make(map[string]string, len(base)+1)
	for k, v := range base {
		a[k] = v
	}
	sev := "info"
	if flag != "" {
		a["dst"] = flag
		sev = "warning"
		r.gap(gapDST)
	}
	err := r.send(line, *ord, KindBoundary, utc, utc, nil, sev, rule, a)
	*ord++
	return err
}

// expandCron emits every boundary of a cron expression inside the window.
func (r *run) expandCron(line int64, ord *int, c *cronSpec, loc *time.Location, rule string, base map[string]string) error {
	if !r.win {
		return nil
	}
	if c.both() {
		r.gap(gapCronBoth)
	}
	lo, hi := r.civilRange(loc)
	for d := lo; !d.After(hi); d = d.AddDate(0, 0, 1) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if !c.dayOK(d.Month(), d.Day(), d.Weekday()) {
			continue
		}
		for _, h := range c.hours {
			for _, m := range c.mins {
				for _, s := range c.secs {
					w := civilWall{d.Year(), d.Month(), d.Day(), h, m, s}
					if err := r.boundaryAt(line, ord, w, loc, rule, base); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
