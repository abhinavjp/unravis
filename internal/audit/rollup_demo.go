package audit

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"
)

// The demo rows cover only the last half hour, which leaves a daily or hourly
// view nearly empty. Rollup therefore adds illustrative history before them:
// counts and latency bins drawn from a fixed formula, not from stored rows. It
// exists so the daily table and the hour grid have something to show in the demo.
// It changes no other demo call.

// demoHistoryHours is how far back the illustrative history reaches.
const demoHistoryHours = MaxRollupDays * 24

func demoHash(sourceID string, unix int64) uint32 {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|%d", sourceID, unix)
	return h.Sum32()
}

// demoSlotVolume is the request count of one hour for one source, by UTC weekday
// and hour: busy on weekdays during the day, quiet at night, and very quiet on
// Sundays so the table shows low-traffic days. Every 17th day is busier, so a
// day-to-day arrow appears.
func demoSlotVolume(sourceID string, hourStart time.Time) int64 {
	h := demoHash(sourceID, hourStart.Unix())
	hour := hourStart.Hour()
	base := int64(4)
	if hour >= 7 && hour < 19 {
		base = 40 + int64(10-abs(hour-13))*4
	}
	switch hourStart.Weekday() {
	case time.Sunday:
		return int64(h % 3)
	case time.Saturday:
		base /= 3
	}
	if (hourStart.Unix()/86400)%17 == 0 {
		base *= 2
	}
	return base + int64(h%7)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// demoRollupHistory adds the illustrative slots that fall before end.
func demoRollupHistory(b *RollupBuilder, plan RollupPlan, sourceID string, end time.Time) {
	from, to := plan.From, plan.To
	if to.After(end) {
		to = end
	}
	floor := end.Add(-demoHistoryHours * time.Hour)
	if from.Before(floor) {
		from = floor
	}
	if !to.After(from) {
		return
	}
	width := int64(plan.Width / time.Second)
	for u := b.SlotStart(from); u < to.Unix(); u += width {
		hourStart := time.Unix(u-u%3600, 0).UTC()
		perHour := demoSlotVolume(sourceID, hourStart)
		slotStart, slotEnd := time.Unix(u, 0).UTC(), time.Unix(u+width, 0).UTC()
		lo, hi := slotStart, slotEnd
		if lo.Before(from) {
			lo = from
		}
		if hi.After(to) {
			hi = to
		}
		if !hi.After(lo) {
			continue
		}
		// The share of this slot inside the window; counts are scaled to it.
		total := perHour * int64(hi.Sub(lo)) / int64(time.Hour)
		if total <= 0 {
			continue
		}
		h := demoHash(sourceID, u)
		background := total / 5
		sync := total * 7 / 10
		c := RollupCounts{Total: total, Sync: sync, Background: background}
		c.C4xx = int64(h%3) * total / 40
		c.C5xx = int64((h>>3)%2) * total / 60
		// Latency: three bins around 120, 240 and 700 ms, so p50 and p95 differ.
		parts := []struct {
			ms    float64
			share int64
		}{{120, 70}, {240, 25}, {700, 5}}
		var left = sync
		for i, part := range parts {
			n := sync * part.share / 100
			if i == len(parts)-1 {
				n = left
			}
			if n <= 0 {
				continue
			}
			left -= n
			ms := part.ms * (1 + float64(h%5)*.02)
			b.AddBin(u, BinIndex(ms), n, ms*float64(n), ms, ms)
			if ms >= plan.SlowMs {
				c.Slow += n
			}
		}
		b.AddCounts(u, c)
	}
}

// Rollup serves the daily and hourly views. The demo is never refused: it has no
// live scan to protect.
func (d *demoRepository) Rollup(ctx context.Context, plan RollupPlan) (RollupSlots, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return RollupSlots{}, err
	}
	for _, id := range plan.SourceIDs {
		if !d.hasSource(id) {
			return RollupSlots{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
	}
	builder := NewRollupBuilder(plan.Width)
	matched := map[string]int64{}
	// The real records are the newest rows; the history stops where they start.
	historyEnd := d.now
	for _, r := range d.records {
		if r.InitiatedAt != nil && r.InitiatedAt.Before(historyEnd) {
			historyEnd = *r.InitiatedAt
		}
	}
	historyEnd = historyEnd.Truncate(time.Second)
	selected := map[string]bool{}
	for _, id := range plan.SourceIDs {
		selected[id] = true
		demoRollupHistory(builder, plan, id, historyEnd)
	}
	for _, r := range d.records {
		if !selected[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(plan.From) || !r.InitiatedAt.Before(plan.To) {
			continue
		}
		state := demoState(r)
		class := StatusClass(r.Kind, state, r.Status)
		builder.AddRow(*r.InitiatedAt, r.Kind, state, class, r.DurationMs, plan.SlowMs)
		matched[r.SourceID]++
	}
	out := RollupSlots{Builder: builder}
	elapsed := time.Since(started)
	for _, id := range plan.SourceIDs {
		out.Sources = append(out.Sources, SourceResult{SourceID: id, OK: true, Version: "demo"})
		out.Provenance = append(out.Provenance, d.demoProvenance(id, "rollup", plan.From, plan.To, demoScope{mode: QueryModeExact}, matched[id], matched[id], elapsed))
	}
	return out, ctx.Err()
}
