package audit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

type LoadWindow struct {
	At     time.Time `json:"at"`
	Count  int64     `json:"count"`
	Timing Stats     `json:"timing"`
}
type SlowestReader interface {
	Slowest(context.Context, PageInput) (Page, error)
}

func ValidateSlowest(input PageInput) error {
	if err := ValidateQuery(input.Query); err != nil {
		return err
	}
	if input.Limit < 1 || input.Limit > 50 || input.Cursor != "" || input.APIKey != "" {
		return fmt.Errorf("%w: invalid slowest request", ErrInvalidInput)
	}
	return nil
}
func RequestMetric(row RequestRecord, metric string) *float64 {
	value := row.DurationMs
	if metric == "api" {
		value = row.ApiTimeMs
	}
	if row.DurationMs == nil || math.IsNaN(*row.DurationMs) || math.IsInf(*row.DurationMs, 0) || *row.DurationMs < 0 || value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return nil
	}
	return value
}
func buildLoadWindows(q Query, buckets []Bucket) ([]LoadWindow, error) {
	type accumulator struct {
		at    time.Time
		count int64
		hist  *histogramAccumulator
	}
	groups := map[string]*accumulator{}
	for _, b := range buckets {
		if (q.Kind != "all" && b.Kind != q.Kind) || (q.Outcome != "all" && b.Outcome != q.Outcome) {
			continue
		}
		key := b.At.UTC().Format(time.RFC3339Nano)
		a := groups[key]
		if a == nil {
			a = &accumulator{at: b.At.UTC(), hist: newHistogramAccumulator()}
			groups[key] = a
		}
		if err := checkedAdd(&a.count, b.Count); err != nil {
			return nil, err
		}
		if b.State == "valid" {
			if err := a.hist.add(b.Bin, b.Count, b.SumMs, b.MinMs, b.MaxMs); err != nil {
				return nil, err
			}
		}
	}
	rows := make([]LoadWindow, 0, len(groups))
	for _, a := range groups {
		rows = append(rows, LoadWindow{At: a.at, Count: a.count, Timing: a.hist.stats()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
	return rows, nil
}
func (d *demoRepository) Slowest(ctx context.Context, input PageInput) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := ValidateSlowest(input); err != nil {
		return Page{}, err
	}
	q := input.Query
	selected := map[string]bool{}
	p := Page{Rows: []RequestRecord{}, Sources: []SourceResult{}}
	for _, id := range q.SourceIDs {
		if !d.hasSource(id) {
			return Page{}, ErrNotFound
		}
		selected[id] = true
		p.Sources = append(p.Sources, SourceResult{SourceID: id, OK: true, Version: "demo"})
	}
	for _, r := range d.records {
		if !selected[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(q.Start) || !r.InitiatedAt.Before(q.End) || (q.Controller != "" && r.Controller != q.Controller) || (q.Action != "" && r.Action != q.Action) || (q.Kind != "all" && r.Kind != q.Kind) || (q.Outcome != "all" && r.Outcome != q.Outcome) || RequestMetric(r, q.Metric) == nil {
			continue
		}
		p.Rows = append(p.Rows, r)
	}
	sort.Slice(p.Rows, func(i, j int) bool {
		a, b := p.Rows[i], p.Rows[j]
		av, bv := *RequestMetric(a, q.Metric), *RequestMetric(b, q.Metric)
		if av != bv {
			return av > bv
		}
		if !a.InitiatedAt.Equal(*b.InitiatedAt) {
			return a.InitiatedAt.After(*b.InitiatedAt)
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.ID > b.ID
	})
	if len(p.Rows) > input.Limit {
		p.Truncated = true
		p.Rows = p.Rows[:input.Limit]
	}
	return p, nil
}
