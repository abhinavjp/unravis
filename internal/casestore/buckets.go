package casestore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Buckets: per-cell event counts of a time grid, by importer, computed either
// by a store's own GROUP BY (BucketCounter) or by streaming the matching
// events through Case.Each (CountBucketsScan), independent of the Events cap.
// This is the data source for the SQL and IIS bucket counts of WP-18b.

// BucketQuery asks for the per-cell event counts of one half-open window.
//
// From and To are the half-open window [From, To): an event at exactly To is
// not counted (Case.Each and EventQuery.Window are inclusive on the end;
// this query is not). AxisStart is the grid anchor; From must not be before
// it. The cell index of an event whose Start is at startMs (Unix
// milliseconds) is (startMs - AxisStart.UnixMilli()) / WidthMs, by integer
// division.
//
// Sources names the importers to count, 1 to 200 of them (required). Kinds
// is an optional filter, 0 to 200 entries.
//
// CountErrors asks for BucketCount.Errors too: the events of a cell whose
// attrs["status"] is exactly three ASCII digits starting with 5 (a 5xx HTTP
// status). Events without such a status (for instance HTTP.sys error markers)
// are counted in Count but never in Errors. Left false, Errors stays 0.
type BucketQuery struct {
	AxisStart, From, To time.Time
	WidthMs             int64
	Sources             []string
	Kinds               []string
	CountErrors         bool
}

// BucketCount is the count of one cell for one importer.
type BucketCount struct {
	Index  int
	Source string
	// ImportID is the imports.id the events came from; it is 0 when the store
	// cannot tell (the scan fallback).
	ImportID int64
	Count    int64
	// Errors is how many of Count have a 5xx status; it is filled only when the
	// query set CountErrors.
	Errors int64
}

// IsServerErrorStatus reports whether v is a 5xx HTTP status: exactly three
// ASCII digits, the first one 5. It is the one definition both bucket paths use.
func IsServerErrorStatus(v string) bool {
	return len(v) == 3 && v[0] == '5' && v[1] >= '0' && v[1] <= '9' && v[2] >= '0' && v[2] <= '9'
}

// BucketResult is the answer to a BucketQuery. Counts is sorted by Index,
// then Source, then ImportID; an empty cell is absent. Total is the sum of
// every Count. Partial is true only when the scan fallback hit ErrScanBudget.
type BucketResult struct {
	Counts  []BucketCount
	Total   int64
	Scanned int64
	Partial bool
}

// ErrBadBucketQuery is the refusal of a BucketQuery that breaks its limits.
var ErrBadBucketQuery = errors.New("invalid bucket query")

// Check reports whether q is well formed: From, To and AxisStart are set, To
// is after From, From is not before AxisStart, WidthMs is positive, the
// window does not need more than MaxSeriesBuckets cells, and Sources and
// Kinds keep to their bounds. Messages name the field only, never a value.
func (q BucketQuery) Check() error {
	switch {
	case q.From.IsZero():
		return fmt.Errorf("%w: From", ErrBadBucketQuery)
	case q.To.IsZero():
		return fmt.Errorf("%w: To", ErrBadBucketQuery)
	case q.AxisStart.IsZero():
		return fmt.Errorf("%w: AxisStart", ErrBadBucketQuery)
	case !q.To.After(q.From):
		return fmt.Errorf("%w: To", ErrBadBucketQuery)
	case q.From.Before(q.AxisStart):
		return fmt.Errorf("%w: From", ErrBadBucketQuery)
	case q.WidthMs <= 0:
		return fmt.Errorf("%w: WidthMs", ErrBadBucketQuery)
	case (q.To.UnixMilli()-q.AxisStart.UnixMilli())/q.WidthMs > MaxSeriesBuckets:
		return fmt.Errorf("%w: WidthMs", ErrBadBucketQuery)
	case len(q.Sources) == 0 || len(q.Sources) > 200:
		return fmt.Errorf("%w: Sources", ErrBadBucketQuery)
	case len(q.Kinds) > 200:
		return fmt.Errorf("%w: Kinds", ErrBadBucketQuery)
	}
	return nil
}

// BucketCounter is implemented by a Case that answers BucketQuery with its own
// GROUP BY, uncapped by the 50 000-row limit of Events. It is a separate
// interface, like Tagger and Quarantiner, so adding it breaks no existing Case.
type BucketCounter interface {
	BucketCounts(ctx context.Context, q BucketQuery) (BucketResult, error)
}

// CountBuckets answers q over c: c's own BucketCounts when c implements
// BucketCounter, else CountBucketsScan.
func CountBuckets(ctx context.Context, c Case, q BucketQuery) (BucketResult, error) {
	if err := q.Check(); err != nil {
		return BucketResult{}, err
	}
	if bc, ok := c.(BucketCounter); ok {
		return bc.BucketCounts(ctx, q)
	}
	return CountBucketsScan(ctx, c, q)
}

// CountBucketsScan answers q by one Case.Each pass, counting per (cell index,
// Source) as events arrive. It is the fallback for a Case that does not
// implement BucketCounter: a Case is not required to honour EventQuery's
// window itself, so every delivered event is checked again here against the
// half-open [From, To). ImportID is always 0: a scan cannot tell which import
// an event came from.
func CountBucketsScan(ctx context.Context, c Case, q BucketQuery) (BucketResult, error) {
	type cell struct {
		index  int
		source string
	}
	counts := map[cell]int64{}
	errCounts := map[cell]int64{}
	fromMs, toMs, axisMs := q.From.UnixMilli(), q.To.UnixMilli(), q.AxisStart.UnixMilli()
	var scanned int64
	eq := EventQuery{
		Window:  importer.Window{From: q.From, To: q.To.Add(-time.Millisecond)},
		Sources: q.Sources,
		Kinds:   q.Kinds,
	}
	err := c.Each(ctx, eq, func(ev evidence.Event) error {
		scanned++
		ts := ev.Start.UnixMilli()
		if ts < fromMs || ts >= toMs {
			return nil
		}
		k := cell{index: int((ts - axisMs) / q.WidthMs), source: ev.Source}
		counts[k]++
		if q.CountErrors && IsServerErrorStatus(ev.Attrs["status"]) {
			errCounts[k]++
		}
		return nil
	})
	partial := false
	switch {
	case err == nil:
	case errors.Is(err, ErrScanBudget):
		partial = true
	default:
		return BucketResult{}, err
	}

	cells := make([]cell, 0, len(counts))
	for k := range counts {
		cells = append(cells, k)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].index != cells[j].index {
			return cells[i].index < cells[j].index
		}
		return cells[i].source < cells[j].source
	})
	res := BucketResult{Counts: make([]BucketCount, len(cells)), Scanned: scanned, Partial: partial}
	for i, k := range cells {
		n := counts[k]
		res.Counts[i] = BucketCount{Index: k.index, Source: k.source, Count: n, Errors: errCounts[k]}
		res.Total += n
	}
	return res, nil
}
