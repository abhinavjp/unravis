package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// MaxSlowestPageSize bounds one page of the slowest-request ranking.
const MaxSlowestPageSize = 50

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
	if input.Limit < 1 || input.Limit > MaxSlowestPageSize || len(input.Cursor) > 4096 || input.APIKey != "" {
		return fmt.Errorf("%w: invalid slowest request", ErrInvalidInput)
	}
	if min := input.MinMs; min != nil && (math.IsNaN(*min) || math.IsInf(*min, 0) || *min < 0) {
		return fmt.Errorf("%w: minimum time must be a non-negative number", ErrInvalidInput)
	}
	return nil
}

// demoSlowCursor is a keyset position in the slowest ranking: metric
// descending, then newest start, then source ascending, then ID descending.
type demoSlowCursor struct {
	Metric      float64   `json:"metric"`
	At          time.Time `json:"at"`
	SourceID    string    `json:"sourceId"`
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
}

func demoSlowBefore(av, bv float64, a, b RequestRecord) bool {
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
		if !KindMatches(q.Kind, b.Kind, b.State == "valid") || !BucketOutcomeMatches(q.Outcome, b.Outcome) {
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
			if err := a.hist.addBucket(b); err != nil {
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
	min := 0.0
	if input.MinMs != nil {
		min = *input.MinMs
	}
	fingerprintBytes, _ := json.Marshal(struct {
		Query Query    `json:"query"`
		MinMs *float64 `json:"minMs"`
		Kind  string   `json:"kind"`
	}{q, input.MinMs, "slowest"})
	sum := sha256.Sum256(fingerprintBytes)
	fingerprint := base64.RawURLEncoding.EncodeToString(sum[:])
	var after *demoSlowCursor
	if input.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(input.Cursor)
		var cursor demoSlowCursor
		if e != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Fingerprint != fingerprint {
			return Page{}, fmt.Errorf("%w: cursor is invalid", ErrInvalidInput)
		}
		after = &cursor
	}
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
		if !selected[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(q.Start) || !r.InitiatedAt.Before(q.End) || (q.Controller != "" && r.Controller != q.Controller) || (q.Action != "" && r.Action != q.Action) || !KindMatches(q.Kind, r.Kind, true) || !BucketOutcomeMatches(q.Outcome, r.Outcome) {
			continue
		}
		value := RequestMetric(r, q.Metric)
		if value == nil || *value < min {
			continue
		}
		if after != nil {
			cursorRow := RequestRecord{SourceID: after.SourceID, ID: after.ID, InitiatedAt: &after.At}
			if !demoSlowBefore(after.Metric, *value, cursorRow, r) {
				continue
			}
		}
		p.Rows = append(p.Rows, r)
	}
	sort.Slice(p.Rows, func(i, j int) bool {
		a, b := p.Rows[i], p.Rows[j]
		return demoSlowBefore(*RequestMetric(a, q.Metric), *RequestMetric(b, q.Metric), a, b)
	})
	if len(p.Rows) > input.Limit {
		p.Truncated = true
		p.Rows = p.Rows[:input.Limit]
		last := p.Rows[len(p.Rows)-1]
		raw, _ := json.Marshal(demoSlowCursor{Metric: *RequestMetric(last, q.Metric), At: last.InitiatedAt.UTC(), SourceID: last.SourceID, ID: last.ID, Fingerprint: fingerprint})
		p.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return p, nil
}
