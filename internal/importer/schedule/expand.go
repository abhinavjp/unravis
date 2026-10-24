package schedule

import (
	"time"

	"api-audit-explorer/internal/importer"
)

// civilRange returns the civil dates (as UTC midnights) that can hold a wall
// time inside the window in loc, with a one-day margin on each side.
func (r *run) civilRange(loc *time.Location) (lo, hi time.Time) {
	f, t := r.from.In(loc), r.to.In(loc)
	lo = time.Date(f.Year(), f.Month(), f.Day()-1, 0, 0, 0, 0, time.UTC)
	hi = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	return lo, hi
}

// spend charges n tested wall-clock times to the file's work budget. Past
// maxWork the file is refused with a fixed LimitError that holds no cell text.
func (r *run) spend(n int64) error {
	r.work += n
	if r.work > maxWork {
		return &importer.LimitError{Limit: "work", Max: maxWork, At: r.work}
	}
	return nil
}

// outsideWindow reports that no wall time in [start, start+span) can fall in
// the window: offsets never exceed 27 hours, so such a span is skipped whole.
func (r *run) outsideWindow(start time.Time, span time.Duration) bool {
	return start.Add(span).Before(r.from.Add(-27*time.Hour)) || start.After(r.to.Add(27*time.Hour))
}

// boundaryAt resolves one scheduled wall time and emits a schedule-boundary
// event for every instant it can mean that falls inside the window. A time in a
// DST fold happens twice and a time in a gap never happens: each reading is
// emitted with a dst flag and a warning severity, and none is guessed.
func (r *run) boundaryAt(line int64, ord *int, w civilWall, loc *time.Location, rule string, base map[string]string) error {
	if err := r.spend(1); err != nil {
		return err
	}
	naive := w.naiveUTC()
	// Offsets never exceed 27 hours from UTC for any zone, so skip cheaply.
	if naive.Before(r.from.Add(-27*time.Hour)) || naive.After(r.to.Add(27*time.Hour)) {
		return nil
	}
	for _, rd := range resolveWallAll(w, loc) {
		if !r.inWindow(rd.utc) {
			continue
		}
		if err := r.countBoundary(); err != nil {
			return err
		}
		a := make(map[string]string, len(base)+2)
		for k, v := range base {
			a[k] = v
		}
		sev := "info"
		if rd.flag != "" {
			a["dst"] = rd.flag
			sev = "warning"
			r.gap(gapDST)
		}
		if rd.utc.After(r.lastB) {
			r.lastB = rd.utc
		}
		err := r.send(line, *ord, KindBoundary, rd.utc, rd.utc, nil, sev, rule, a)
		*ord++
		if err != nil {
			return err
		}
	}
	return nil
}

// expandCron emits every boundary of a cron expression inside the window. A
// schedule that reaches the boundary cap is cut and the rest of the window is
// marked unknown; the file is still imported.
func (r *run) expandCron(line int64, ord *int, c *cronSpec, loc *time.Location, rule string, base map[string]string) error {
	if !r.win {
		return nil
	}
	if c.both() {
		r.gap(gapCronBoth)
	}
	r.beginSchedule()
	if r.nCapped >= maxBoundaries-1 {
		// The file already holds its share of boundaries: cut this schedule
		// without testing any wall time.
		return r.cutSchedule(errBoundaryCap, line, ord, base["job"], rule)
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
			if r.outsideWindow(d.Add(time.Duration(h)*time.Hour), time.Hour) {
				continue
			}
			for _, m := range c.mins {
				if r.outsideWindow(d.Add(time.Duration(h)*time.Hour+time.Duration(m)*time.Minute), time.Minute) {
					continue
				}
				for _, s := range c.secs {
					w := civilWall{d.Year(), d.Month(), d.Day(), h, m, s}
					if err := r.boundaryAt(line, ord, w, loc, rule, base); err != nil {
						return r.cutSchedule(err, line, ord, base["job"], rule)
					}
				}
			}
		}
	}
	return nil
}
