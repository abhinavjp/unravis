package perfdata

import (
	"math"
	"math/rand/v2"
	"time"
)

// The plan decides, before a single row exists, how many IIS and SQL rows each
// minute of the window holds. It is a pure function of the Config: no clock, no
// map order, no shared state. Totals are hit exactly by cumulative rounding.

const minutesPerDay = 1440

// Random streams: one PCG per (stream, key) pair, so a day never depends on
// another day's draws.
const (
	streamPlan   uint64 = 0
	streamIIS    uint64 = 1
	streamSQL    uint64 = 2
	streamBursts uint64 = 3
	streamOutage uint64 = 4
	streamPlanSQ uint64 = 5
)

func newRand(seed, stream, key uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed^(stream*0x9E3779B97F4A7C15), key))
}

// Burst is a short stretch of heavy traffic with many server errors.
type Burst struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Multiplier int       `json:"multiplier"`
}

// Outage is a window in which neither lane has a row.
type Outage struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

const (
	burstMultIIS = 8
	burstMultSQL = 20
)

// DayPlan is what one UTC day holds.
type DayPlan struct {
	Day       time.Time
	Index     int
	Factor    float64
	IIS, SQL  int64
	IISMin    [minutesPerDay]int32
	SQLMin    [minutesPerDay]int32
	BurstMin  [minutesPerDay]bool
	OutageMin [minutesPerDay]bool
}

// Plan is the whole window.
type Plan struct {
	cfg     Config
	Bursts  []Burst
	Outages []Outage
	days    []DayPlan
}

var minuteShape = func() [minutesPerDay]float64 {
	var s [minutesPerDay]float64
	for m := range s {
		h := float64(m) / 60
		a := math.Exp(-math.Pow(h-10, 2) / (2 * 1.8 * 1.8))
		b := math.Exp(-math.Pow(h-15, 2) / (2 * 2 * 2))
		s[m] = 0.05 + a + 0.9*b
	}
	return s
}()

// PlanDay is the plan of one day of the window (0 is the first day).
func PlanDay(cfg Config, day int) (DayPlan, error) {
	if err := cfg.Normalize(); err != nil {
		return DayPlan{}, err
	}
	p := BuildPlan(cfg)
	if day < 0 || day >= len(p.days) {
		return DayPlan{}, errDayRange
	}
	return p.days[day], nil
}

// Days is the day plans in order.
func (p *Plan) Days() []DayPlan { return p.days }

func epochDay(t time.Time) uint64 { return uint64(t.Unix() / 86400) }

// BuildPlan plans the window of a normalized Config.
func BuildPlan(cfg Config) *Plan {
	p := &Plan{cfg: cfg}
	start, end := cfg.Start(), cfg.WindowEnd()
	p.Outages = planOutages(cfg, start, end)
	p.Bursts = planBursts(cfg, start, end, p.Outages)

	p.days = make([]DayPlan, cfg.Days())
	iisW := make([][minutesPerDay]float64, cfg.Days())
	sqlW := make([][minutesPerDay]float64, cfg.Days())
	var iisTotal, sqlTotal float64
	for d := range p.days {
		day := start.AddDate(0, 0, d)
		dp := &p.days[d]
		dp.Day, dp.Index = day, d
		r := newRand(cfg.Seed, streamPlan, epochDay(day))
		f := 0.92 + 0.16*r.Float64()
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			f *= 0.25
		}
		if day.Day() > daysIn(day.Year(), day.Month())-3 {
			f *= 1.5
		}
		dp.Factor = f
		markMinutes(dp, p.Bursts, p.Outages)
		rs := newRand(cfg.Seed, streamPlanSQ, epochDay(day))
		for m := 0; m < minutesPerDay; m++ {
			if dp.OutageMin[m] {
				continue
			}
			wi := minuteShape[m] * f * (0.85 + 0.3*r.Float64())
			ws := minuteShape[m] * f * (0.85 + 0.3*rs.Float64())
			if dp.BurstMin[m] {
				wi *= burstMultIIS
				ws *= burstMultSQL
			}
			iisW[d][m], sqlW[d][m] = wi, ws
			iisTotal += wi
			sqlTotal += ws
		}
	}
	p.distribute(iisW, iisTotal, cfg.IISRows, func(dp *DayPlan) (*int64, *[minutesPerDay]int32) { return &dp.IIS, &dp.IISMin })
	p.distribute(sqlW, sqlTotal, cfg.SQLRows, func(dp *DayPlan) (*int64, *[minutesPerDay]int32) { return &dp.SQL, &dp.SQLMin })
	return p
}

// distribute spreads total rows over the days by the weights, then each day's
// rows over its minutes, by cumulative rounding so the sums are exact and a
// zero-weight minute gets nothing.
func (p *Plan) distribute(w [][minutesPerDay]float64, sum float64, total int64, pick func(*DayPlan) (*int64, *[minutesPerDay]int32)) {
	if sum <= 0 {
		return
	}
	dayW := make([]float64, len(w))
	for d := range w {
		for _, x := range w[d] {
			dayW[d] += x
		}
	}
	var cum float64
	var prev int64
	for d := range p.days {
		cum += dayW[d]
		cur := int64(math.Floor(cum/sum*float64(total) + 0.5))
		if d == len(p.days)-1 {
			cur = total
		}
		n, mins := pick(&p.days[d])
		*n = cur - prev
		prev = cur
		if *n == 0 || dayW[d] == 0 {
			continue
		}
		var mc float64
		var mprev int64
		for m := 0; m < minutesPerDay; m++ {
			mc += w[d][m]
			mcur := int64(math.Floor(mc/dayW[d]*float64(*n) + 0.5))
			if m == minutesPerDay-1 {
				mcur = *n
			}
			mins[m] = int32(mcur - mprev)
			mprev = mcur
		}
	}
}

func markMinutes(dp *DayPlan, bursts []Burst, outages []Outage) {
	day0 := dp.Day
	day1 := day0.Add(24 * time.Hour)
	mark := func(s, e time.Time, dst *[minutesPerDay]bool) {
		if !s.Before(day1) || !e.After(day0) {
			return
		}
		a, b := 0, minutesPerDay
		if s.After(day0) {
			a = int(s.Sub(day0) / time.Minute)
		}
		if e.Before(day1) {
			b = int(e.Sub(day0) / time.Minute)
		}
		for m := a; m < b; m++ {
			dst[m] = true
		}
	}
	for _, b := range bursts {
		mark(b.Start, b.End, &dp.BurstMin)
	}
	for _, o := range outages {
		mark(o.Start, o.End, &dp.OutageMin)
	}
}

// planOutages gives each calendar month one two-hour outage on a whole hour,
// kept when it lies wholly inside the window. A window with none (a short one)
// gets one on its second day.
func planOutages(cfg Config, start, end time.Time) []Outage {
	var out []Outage
	y, m, _ := start.Date()
	for {
		first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		if !first.Before(end) {
			break
		}
		r := newRand(cfg.Seed, streamOutage, uint64(y*12+int(m)))
		dom := 3 + r.IntN(25)
		hour := r.IntN(22)
		s := time.Date(y, m, dom, hour, 0, 0, 0, time.UTC)
		if e := s.Add(2 * time.Hour); !s.Before(start) && !e.After(end) {
			out = append(out, Outage{Start: s, End: e})
		}
		m++
		if m > 12 {
			m, y = 1, y+1
		}
	}
	if len(out) == 0 {
		s := start.Add(24*time.Hour + 3*time.Hour)
		if !s.Add(2 * time.Hour).After(end) {
			out = append(out, Outage{Start: s, End: s.Add(2 * time.Hour)})
		}
	}
	return out
}

// planBursts gives every week two to four bursts of 5 to 20 minutes.
func planBursts(cfg Config, start, end time.Time, outages []Outage) []Burst {
	var out []Burst
	firstWeek := (epochDay(start) + 3) / 7
	lastWeek := (epochDay(end) + 3) / 7
	for w := firstWeek; w <= lastWeek; w++ {
		r := newRand(cfg.Seed, streamBursts, w)
		n := 2 + r.IntN(3)
		weekStart := time.Unix(int64(w*7-3)*86400, 0).UTC()
		for i := 0; i < n; i++ {
			dayOff := r.IntN(7)
			minute := r.IntN(minutesPerDay - 20)
			dur := 5 + r.IntN(16)
			s := weekStart.AddDate(0, 0, dayOff).Add(time.Duration(minute) * time.Minute)
			b := Burst{Start: s, End: s.Add(time.Duration(dur) * time.Minute), Multiplier: burstMultIIS}
			if b.Start.Before(start) || b.End.After(end) {
				continue
			}
			clash := false
			for _, o := range outages {
				if b.Start.Before(o.End) && b.End.After(o.Start) {
					clash = true
				}
			}
			for _, x := range out {
				if b.Start.Before(x.End) && b.End.After(x.Start) {
					clash = true
				}
			}
			if !clash {
				out = append(out, b)
			}
		}
	}
	return out
}
