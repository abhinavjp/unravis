package audit

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"
)

// ErrRollupGroupLimit is returned (wrapped in ErrLimit) when one source returns
// more than MaxRollupGroups time slot and latency bin groups.
var ErrRollupGroupLimit = errors.New("rollup group limit exceeded")

// LiveRollupMaxWindow is the longest window a rollup may read from a live source
// unless broad scans are allowed: a longer one reads every request in it.
const LiveRollupMaxWindow = 24 * time.Hour

// Daily and hourly rollups cover far longer windows than the 7-day MaxInterval of
// the other endpoints. They have their own limits and never lift that one.
const (
	// MaxRollupDays is the longest rollup window, in 24-hour days.
	MaxRollupDays   = 120
	MaxRollupWindow = MaxRollupDays * 24 * time.Hour
	// MaxRollupGroups bounds the (time slot x latency bin) groups one source may
	// return. It fails closed: a cut list would make every later day wrong.
	MaxRollupGroups = 600000
	// RollupLowTraffic is the request count below which a day is low-traffic: it
	// has no arrow and is never the base another day is compared with.
	RollupLowTraffic = 100
	// RollupNoiseCount is the request count below which an hour cell is noise.
	RollupNoiseCount = 5
	// RollupFlatPercent is the change (in percent of the base day) under which the
	// arrow is flat. A change of exactly this size counts as up or down.
	RollupFlatPercent = 10
	// DefaultRollupSlowMs is the duration from which a request counts as slow in
	// an hour cell, and MaxRollupSlowMs the largest value a caller may ask for.
	DefaultRollupSlowMs = 1000.0
	MaxRollupSlowMs     = 3600000.0

	// rollupMinSlot is the narrowest time slot a rollup reads. A zone whose
	// offsets need a finer slot (a historical local mean time) is refused.
	rollupMinSlot = 15 * time.Minute
)

// RollupInput selects the sources, the window and the display zone. SlowMs zero
// means DefaultRollupSlowMs.
type RollupInput struct {
	SourceIDs []string
	From, To  time.Time
	TimeZone  string
	SlowMs    float64
}

// RollupPlan is a validated RollupInput. Width is the length of the UTC time slots
// the sources aggregate into: the greatest common divisor of one hour and every
// UTC offset the zone has in the window, so a slot never straddles a local hour
// (60, 30 or 15 minutes). Days and hours are folded from slots afterwards, which
// keeps a 23-hour or 25-hour day exact.
type RollupPlan struct {
	SourceIDs []string
	From, To  time.Time
	Zone      string
	Loc       *time.Location
	SlowMs    float64
	Width     time.Duration
}

// PlanRollup validates the input.
func PlanRollup(in RollupInput) (RollupPlan, error) {
	if len(in.SourceIDs) == 0 || len(in.SourceIDs) > MaxSources {
		return RollupPlan{}, invalidInputf("sources must contain 1 to %d sources", MaxSources)
	}
	seen := make(map[string]struct{}, len(in.SourceIDs))
	for _, id := range in.SourceIDs {
		if id == "" || len(id) > 128 {
			return RollupPlan{}, invalidInputf("sources must not contain blank or very long values")
		}
		if _, dup := seen[id]; dup {
			return RollupPlan{}, invalidInputf("sources must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	if in.From.IsZero() || in.To.IsZero() {
		return RollupPlan{}, invalidInputf("from and to are required")
	}
	from, to := in.From.UTC(), in.To.UTC()
	if from.Year() < 1970 || to.Year() > 9000 {
		return RollupPlan{}, invalidInputf("from and to must be between 1970 and 9000")
	}
	if !to.After(from) {
		return RollupPlan{}, invalidInputf("from must be before to")
	}
	if to.Sub(from) > MaxRollupWindow {
		return RollupPlan{}, invalidInputf("the window must not exceed %d days", MaxRollupDays)
	}
	slowMs := in.SlowMs
	switch {
	case math.IsNaN(slowMs) || math.IsInf(slowMs, 0) || slowMs < 0 || slowMs > MaxRollupSlowMs:
		return RollupPlan{}, invalidInputf("slowMs must be a number between 1 and %d", int64(MaxRollupSlowMs))
	case slowMs == 0:
		slowMs = DefaultRollupSlowMs
	case slowMs < 1:
		return RollupPlan{}, invalidInputf("slowMs must be a number between 1 and %d", int64(MaxRollupSlowMs))
	}
	loc, err := LoadZone(in.TimeZone)
	if err != nil {
		return RollupPlan{}, err
	}
	zone := in.TimeZone
	if zone == "" {
		zone = "UTC"
	}
	width, err := rollupSlotWidth(loc, from, to)
	if err != nil {
		return RollupPlan{}, err
	}
	return RollupPlan{
		SourceIDs: append([]string(nil), in.SourceIDs...), From: from, To: to,
		Zone: zone, Loc: loc, SlowMs: slowMs, Width: width,
	}, nil
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// rollupSlotWidth is the gcd of one hour and the zone's UTC offsets from a day
// before the window to a day after it, sampled hourly (a transition is never
// closer to the next than that).
func rollupSlotWidth(loc *time.Location, from, to time.Time) (time.Duration, error) {
	g := int64(3600)
	for t := from.Add(-24 * time.Hour).Truncate(time.Hour); !t.After(to.Add(24 * time.Hour)); t = t.Add(time.Hour) {
		_, offset := t.In(loc).Zone()
		g = gcd(g, int64(offset))
	}
	width := time.Duration(g) * time.Second
	if width < rollupMinSlot {
		return 0, invalidInputf("this time zone has no usable whole-minute offsets for these dates")
	}
	return width, nil
}

// RollupCounts are the counters of one time slot. Sync and Background are the
// synchronous and Hangfire rows; Total also holds asynchronous and unknown rows.
// Slow counts the latency sample rows at or above the slow limit.
type RollupCounts struct {
	Total, Sync, Background, C4xx, C5xx, Slow int64
}

func (c *RollupCounts) merge(o RollupCounts) {
	c.Total += o.Total
	c.Sync += o.Sync
	c.Background += o.Background
	c.C4xx += o.C4xx
	c.C5xx += o.C5xx
	c.Slow += o.Slow
}

type rollupBin struct {
	count         int64
	sum, min, max float64
}

type rollupSlot struct {
	counts RollupCounts
	bins   map[int]*rollupBin
}

// RollupBuilder merges per-slot counters and latency histogram bins from any
// number of sources. The bins hold the latency sample only: valid, synchronous
// rows. A server-side reader feeds AddCounts and AddBin; a scan done in Go feeds
// AddRow.
type RollupBuilder struct {
	width  int64 // seconds
	slots  map[int64]*rollupSlot
	groups int64
}

// NewRollupBuilder returns a builder for slots of the given width.
func NewRollupBuilder(width time.Duration) *RollupBuilder {
	return &RollupBuilder{width: int64(width / time.Second), slots: map[int64]*rollupSlot{}}
}

func (b *RollupBuilder) slot(unix int64) *rollupSlot {
	s := b.slots[unix]
	if s == nil {
		s = &rollupSlot{bins: map[int]*rollupBin{}}
		b.slots[unix] = s
	}
	return s
}

// SlotStart is the start of the slot that holds t, in Unix seconds.
func (b *RollupBuilder) SlotStart(t time.Time) int64 {
	u := t.Unix()
	return u - ((u%b.width)+b.width)%b.width
}

// AddCounts merges the counters of the slot that starts at unix seconds.
func (b *RollupBuilder) AddCounts(unix int64, c RollupCounts) {
	b.slot(unix).counts.merge(c)
	b.groups++
}

func addRollupBin(bins map[int]*rollupBin, bin int, count int64, sum, min, max float64) {
	cur := bins[bin]
	if cur == nil {
		bins[bin] = &rollupBin{count: count, sum: sum, min: min, max: max}
		return
	}
	cur.count += count
	cur.sum += sum
	cur.min = math.Min(cur.min, min)
	cur.max = math.Max(cur.max, max)
}

// AddBin merges one latency bin of the slot that starts at unix seconds.
func (b *RollupBuilder) AddBin(unix int64, bin int, count int64, sum, min, max float64) {
	addRollupBin(b.slot(unix).bins, bin, count, sum, min, max)
}

// AddRow counts one request row. state is "valid", "invalid" or "incomplete" and
// class is its status class. A row enters the latency sample only when it is a
// valid synchronous row with a usable duration.
func (b *RollupBuilder) AddRow(at time.Time, kind, state, class string, durationMs *float64, slowMs float64) {
	s := b.slot(b.SlotStart(at))
	s.counts.Total++
	switch kind {
	case "synchronous":
		s.counts.Sync++
	case "hangfire":
		s.counts.Background++
	}
	switch class {
	case StatusClientError:
		s.counts.C4xx++
	case StatusServerError:
		s.counts.C5xx++
	}
	if kind == "synchronous" && state == "valid" && durationMs != nil {
		if bin := BinIndex(*durationMs); bin >= 0 {
			addRollupBin(s.bins, bin, 1, *durationMs, *durationMs, *durationMs)
			if *durationMs >= slowMs {
				s.counts.Slow++
			}
		}
	}
}

// Groups is the number of aggregated groups merged so far (rows read from a
// server-side aggregation).
func (b *RollupBuilder) Groups() int64 { return b.groups }

// Merge folds another builder of the same width into b.
func (b *RollupBuilder) Merge(o *RollupBuilder) {
	for unix, s := range o.slots {
		dst := b.slot(unix)
		dst.counts.merge(s.counts)
		for bin, v := range s.bins {
			addRollupBin(dst.bins, bin, v.count, v.sum, v.min, v.max)
		}
	}
	b.groups += o.groups
}

// RollupReader is an optional Repository capability. It returns the merged time
// slots of the plan's window, one aggregation pass per source.
type RollupReader interface {
	Rollup(ctx context.Context, plan RollupPlan) (RollupSlots, error)
}

// RollupSlots is what a repository returns: the merged slots and what each source
// did. Sources that failed are listed with their error code and add nothing.
type RollupSlots struct {
	Builder    *RollupBuilder
	Sources    []SourceResult
	Provenance []Provenance
}

// slotRef is one time slot of the window, cut to the window, with the local day
// and hour it falls in.
type slotRef struct {
	unix       int64
	start, end time.Time
	date       string
	hour       int
}

func (p RollupPlan) slotRefs() []slotRef {
	width := int64(p.Width / time.Second)
	first := p.From.Unix() - ((p.From.Unix()%width)+width)%width
	var refs []slotRef
	for u := first; u < p.To.Unix() || (u == p.To.Unix() && p.To.Nanosecond() > 0); u += width {
		start, end := time.Unix(u, 0).UTC(), time.Unix(u+width, 0).UTC()
		local := start.In(p.Loc)
		if start.Before(p.From) {
			start = p.From
		}
		if end.After(p.To) {
			end = p.To
		}
		refs = append(refs, slotRef{unix: u, start: start, end: end, date: local.Format("2006-01-02"), hour: local.Hour()})
	}
	return refs
}

// localDayBounds is the whole local day that holds t: from its first instant to
// the first instant of the next day. Its length is 23, 24 or 25 hours across a
// DST change.
func localDayBounds(t time.Time, loc *time.Location) (time.Time, time.Time) {
	y, m, d := t.In(loc).Date()
	return localDayStart(y, m, d, loc), localDayStart(y, m, d+1, loc)
}

// localDayStart is the first instant of a local date. time.Date places a
// midnight that a DST change skipped just after the gap; if a zone ever gives an
// earlier date, step forward until the local date matches.
func localDayStart(y int, m time.Month, d int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	want := time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Format("2006-01-02")
	for i := 0; i < 180 && t.In(loc).Format("2006-01-02") < want; i++ {
		t = t.Add(time.Minute)
	}
	return t.UTC()
}

// Direction values of RollupDay.VsPrev.
const (
	TrendUp   = "up"
	TrendDown = "down"
	TrendFlat = "flat"
)

// RollupDay is one local-zone day of the window. Start and End are UTC and are
// cut to the window; Hours is the length of the whole local day (23, 24 or 25),
// and Partial is set when the window cuts it. Background is the Hangfire count
// and Other the rest (asynchronous and unknown). P50 and P95 come from valid
// synchronous rows only (LatencyCount of them) and are nil below MinSampleCount.
// VsPrev compares Total with the nearest earlier day that is neither
// low-traffic nor partial (VsPrevDate), and is empty for a low-traffic or
// partial day, or when no such earlier day exists.
type RollupDay struct {
	Date         string    `json:"date"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Hours        float64   `json:"hours"`
	Partial      bool      `json:"partial"`
	Total        int64     `json:"total"`
	Sync         int64     `json:"sync"`
	Background   int64     `json:"background"`
	Other        int64     `json:"other"`
	LatencyCount int64     `json:"latencyCount"`
	P50          *Quantile `json:"p50"`
	P95          *Quantile `json:"p95"`
	Count4xx     int64     `json:"count4xx"`
	Count5xx     int64     `json:"count5xx"`
	LowTraffic   bool      `json:"lowTraffic"`
	VsPrev       string    `json:"vsPrev"`
	VsPrevPct    *float64  `json:"vsPrevPct"`
	VsPrevDate   string    `json:"vsPrevDate,omitempty"`
}

func sumCounts(b *RollupBuilder, refs []slotRef) (RollupCounts, *histogramAccumulator, error) {
	var c RollupCounts
	acc := newHistogramAccumulator()
	for _, ref := range refs {
		s := b.slots[ref.unix]
		if s == nil {
			continue
		}
		c.merge(s.counts)
		bins := make([]int, 0, len(s.bins))
		for bin := range s.bins {
			bins = append(bins, bin)
		}
		sort.Ints(bins)
		for _, bin := range bins {
			v := s.bins[bin]
			if err := acc.add(bin, v.count, v.sum, v.min, v.max); err != nil {
				return c, nil, err
			}
		}
	}
	return c, acc, nil
}

// BuildDaily folds the slots into local days and marks each day against the
// previous busy day.
func BuildDaily(plan RollupPlan, b *RollupBuilder) ([]RollupDay, error) {
	refs := plan.slotRefs()
	days := []RollupDay{}
	for i := 0; i < len(refs); {
		j := i
		for j < len(refs) && refs[j].date == refs[i].date {
			j++
		}
		group := refs[i:j]
		i = j
		counts, acc, err := sumCounts(b, group)
		if err != nil {
			return nil, err
		}
		start, end := group[0].start, group[len(group)-1].end
		fullStart, fullEnd := localDayBounds(group[0].start, plan.Loc)
		day := RollupDay{
			Date: group[0].date, Start: start, End: end,
			Hours:   fullEnd.Sub(fullStart).Hours(),
			Partial: start.After(fullStart) || end.Before(fullEnd),
			Total:   counts.Total, Sync: counts.Sync, Background: counts.Background,
			Other:        counts.Total - counts.Sync - counts.Background,
			LatencyCount: acc.count,
			Count4xx:     counts.C4xx, Count5xx: counts.C5xx,
			LowTraffic: counts.Total < RollupLowTraffic,
		}
		if acc.count >= MinSampleCount {
			day.P50, day.P95 = acc.quantile(.50), acc.quantile(.95)
		}
		days = append(days, day)
	}
	markTrend(days)
	return days, nil
}

// markTrend sets VsPrev. The base of a day is the nearest earlier day that is
// not low-traffic and not partial; a low-traffic or partial day is never a base
// and gets no arrow itself.
func markTrend(days []RollupDay) {
	base := -1
	for i := range days {
		d := &days[i]
		if d.LowTraffic || d.Partial {
			continue
		}
		if base >= 0 {
			prev := days[base]
			diff := d.Total - prev.Total
			pct := float64(diff) / float64(prev.Total) * 100
			d.VsPrevPct, d.VsPrevDate = &pct, prev.Date
			switch {
			case diff > 0 && diff*100 >= prev.Total*RollupFlatPercent:
				d.VsPrev = TrendUp
			case diff < 0 && -diff*100 >= prev.Total*RollupFlatPercent:
				d.VsPrev = TrendDown
			default:
				d.VsPrev = TrendFlat
			}
		}
		base = i
	}
}

// HourCell is one local hour of one local day. Start and End are the UTC bounds
// of the requests it counts, cut to the window; setting a query window to them
// selects the same requests. Count is every request, LatencyCount the valid
// synchronous ones, and P95 and SlowCount come from those only. P95 is nil below
// RollupNoiseCount such rows. A local hour that a DST change repeats is one cell
// that spans both; an hour it skips has no cell. Noise is Count under
// RollupNoiseCount.
type HourCell struct {
	Date         string    `json:"date"`
	Hour         int       `json:"hour"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Count        int64     `json:"count"`
	LatencyCount int64     `json:"latencyCount"`
	P95          *Quantile `json:"p95"`
	SlowCount    int64     `json:"slowCount"`
	Noise        bool      `json:"noise"`
}

// HourGridDay is a row of the grid: a local date, the length of the whole local
// day and whether the window cuts it.
type HourGridDay struct {
	Date    string  `json:"date"`
	Hours   float64 `json:"hours"`
	Partial bool    `json:"partial"`
}

// HourGrid is every local hour of the window, in time order, in a day x hour
// layout. Cells with no requests are included (Count 0, Noise true).
type HourGrid struct {
	Days  []HourGridDay `json:"days"`
	Cells []HourCell    `json:"cells"`
}

// BuildHourGrid folds the slots into local day x hour cells.
func BuildHourGrid(plan RollupPlan, b *RollupBuilder) (HourGrid, error) {
	refs := plan.slotRefs()
	grid := HourGrid{Days: []HourGridDay{}, Cells: []HourCell{}}
	lastDate := ""
	for i := 0; i < len(refs); {
		j := i
		for j < len(refs) && refs[j].date == refs[i].date && refs[j].hour == refs[i].hour {
			j++
		}
		group := refs[i:j]
		i = j
		counts, acc, err := sumCounts(b, group)
		if err != nil {
			return HourGrid{}, err
		}
		cell := HourCell{
			Date: group[0].date, Hour: group[0].hour,
			Start: group[0].start, End: group[len(group)-1].end,
			Count: counts.Total, LatencyCount: acc.count, SlowCount: counts.Slow,
			Noise: counts.Total < RollupNoiseCount,
		}
		if acc.count >= RollupNoiseCount {
			cell.P95 = acc.quantile(.95)
		}
		if cell.Date != lastDate {
			lastDate = cell.Date
			fullStart, fullEnd := localDayBounds(cell.Start, plan.Loc)
			grid.Days = append(grid.Days, HourGridDay{Date: cell.Date, Hours: fullEnd.Sub(fullStart).Hours()})
		}
		grid.Cells = append(grid.Cells, cell)
	}
	// A day is partial when the window cuts it: its first or last instant.
	for i := range grid.Days {
		fullStart, fullEnd := localDayBounds(firstCellStart(grid.Cells, grid.Days[i].Date, plan.Loc), plan.Loc)
		grid.Days[i].Partial = plan.From.After(fullStart) || plan.To.Before(fullEnd)
	}
	return grid, nil
}

func firstCellStart(cells []HourCell, date string, loc *time.Location) time.Time {
	for _, c := range cells {
		if c.Date == date {
			return c.Start
		}
	}
	return time.Time{}
}
