package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const queryTimeout = audit.OperationTimeout
const maxAnalysisBytes = 16 * 1024 * 1024

type bucketDocument struct {
	Controller string    `bson:"controller"`
	Action     string    `bson:"action"`
	Kind       string    `bson:"kind"`
	Outcome    string    `bson:"outcome"`
	State      string    `bson:"state"`
	At         time.Time `bson:"at"`
	Bin        int       `bson:"bin"`
	Count      int64     `bson:"count"`
	SumMs      float64   `bson:"sumMs"`
	MinMs      float64   `bson:"minMs"`
	MaxMs      float64   `bson:"maxMs"`
	// Phase sums and per-phase sample counts.
	SumAuditMs   float64 `bson:"sumAuditMs"`
	SumApiMs     float64 `bson:"sumApiMs"`
	SumPayloadMs float64 `bson:"sumPayloadMs"`
	NAudit       int64   `bson:"nAudit"`
	NApi         int64   `bson:"nApi"`
	NPayload     int64   `bson:"nPayload"`

	PendingAsync    int64 `bson:"pendingAsync"`
	ObservedPartial int64 `bson:"observedPartial"`

	C4xx            int64 `bson:"c4xx"`
	C5xx            int64 `bson:"c5xx"`
	CHangfireFailed int64 `bson:"cHangfireFailed"`
}

type sourceAnalysis struct {
	result    audit.SourceResult
	current   []audit.Bucket
	reference []audit.Bucket
	timings   []audit.SourceTiming
	// provenance holds one entry per window the source was read for; a source that
	// did not run (admission failed) carries one failed "current" entry.
	provenance []audit.Provenance
	err        error
}

// Analyze runs current and fixed reference aggregations for each selected source.
func (s *Store) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	if err := audit.ValidateQuery(q); err != nil {
		return audit.Analysis{}, fmt.Errorf("%w: %v", audit.ErrInvalidInput, err)
	}
	if q.HasAgeRange() {
		// One clock for every source and both intervals, rendered as a literal.
		q.Now = s.clock()
	}
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	workCtx, cancelWork := context.WithCancel(queryCtx)
	defer cancelWork()
	results := make([]sourceAnalysis, len(q.SourceIDs))
	profiles := make([]*profile, len(q.SourceIDs))
	releases := make([]func(), len(q.SourceIDs))
	for i, id := range q.SourceIDs {
		p, release, err := s.leaseSource(id)
		if err != nil {
			for _, done := range releases {
				if done != nil {
					done()
				}
			}
			return audit.Analysis{}, err
		}
		profiles[i], releases[i] = p, release
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	var rows atomic.Int64
	var rowBytes atomic.Int64
	var legacyRecords atomic.Int64
	var legacyBytes atomic.Int64
	var workers sync.WaitGroup
	for i, p := range profiles {
		workers.Add(1)
		go func(i int, p *profile) {
			defer workers.Done()
			notRun := func(code string) []audit.Provenance {
				return []audit.Provenance{failedProvenance(p, "current", q.Start, q.End, audit.QueryModeExact, nil, notRunNote(q.QueryMode), code, 0)}
			}
			release, denied := s.admit(workCtx, p)
			if denied != nil {
				if denied.Stage == admitRemoved {
					results[i] = failedSource(p, denied.Message)
				} else {
					results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, Version: p.version, ErrorCode: denied.Code, Error: denied.Message}}
				}
				results[i].provenance = notRun(denied.Code)
				return
			}
			defer release()
			// Hint mode: calibrate once per source, after admission. When it cannot
			// run the source is read exact and each entry says why.
			margin, note := s.resolveHint(workCtx, p, q.QueryMode, "")
			ranMode := audit.QueryModeExact
			if margin != nil {
				ranMode = audit.QueryModeHint
			}
			var timings []audit.SourceTiming
			var provenance []audit.Provenance
			timed := func(window string, start, end time.Time) ([]audit.Bucket, error) {
				began := time.Now()
				buckets, err := s.readBuckets(workCtx, p, q, start, end, margin, &rows, &rowBytes, &legacyRecords, &legacyBytes)
				elapsed := time.Since(began)
				timings = append(timings, audit.SourceTiming{SourceID: p.source.ID, Window: window, ReadMs: elapsed.Milliseconds(), Rows: int64(len(buckets))})
				pv := newProvenance(p, window, start, end, ranMode, margin)
				pv.Note = note
				code := ""
				if err != nil {
					code, _ = sourceQueryError(queryCtx, err)
				}
				var sample int64
				for _, b := range buckets {
					sample += b.Count
				}
				finishProvenance(&pv, int64(len(buckets)), sample, elapsed, code)
				provenance = append(provenance, pv)
				return buckets, err
			}
			current, err := timed("current", q.Start, q.End)
			var reference []audit.Bucket
			if err == nil {
				reference, err = timed("reference", q.BaselineStart, q.BaselineEnd)
			}
			if err == nil {
				results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: true, Version: p.version, IndexWarning: p.indexWarning}, current: current, reference: reference, timings: timings, provenance: provenance}
				return
			}
			if errors.Is(err, audit.ErrLimit) {
				cancelWork()
				results[i] = sourceAnalysis{err: err}
				return
			}
			code, message := sourceQueryError(queryCtx, err)
			results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, Version: p.version, ErrorCode: code, Error: message}, timings: timings, provenance: provenance, err: err}
		}(i, p)
	}
	workers.Wait()
	for _, result := range results {
		if errors.Is(result.err, audit.ErrLimit) {
			return audit.Analysis{}, result.err
		}
	}

	current := make([]audit.Bucket, 0)
	reference := make([]audit.Bucket, 0)
	sourceResults := make([]audit.SourceResult, 0, len(results))
	var timings []audit.SourceTiming
	var provenance []audit.Provenance
	for _, result := range results {
		sourceResults = append(sourceResults, result.result)
		timings = append(timings, result.timings...)
		provenance = append(provenance, result.provenance...)
		if result.result.OK {
			current = append(current, result.current...)
			reference = append(reference, result.reference...)
		}
	}
	analysis, err := audit.BuildAnalysis(q, current, reference, sourceResults)
	if err != nil {
		return audit.Analysis{}, err
	}
	analysis.Timings = timings
	analysis.Provenance = provenance
	analysis.Warnings = append(analysis.Warnings, "Legacy records without RequestInitiatedAt use CreatedOn when it contains a BSON date.")
	serialized, err := json.Marshal(analysis)
	if err != nil || len(serialized) > maxAnalysisBytes {
		return audit.Analysis{}, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrAnalysisBytesLimit)
	}
	return analysis, nil
}

// estimateBucketJSON is a cheap upper bound on len(json.Marshal(bucket)): no
// allocation and no reflection, so the early byte bail-out in readBuckets costs
// a few additions per bucket instead of an encode. It never under-counts, so the
// bail-out cannot let through more than json.Marshal would have; the exact check
// on the whole Analysis (in Analyze) is what decides the final size. ok is false
// for a bucket json.Marshal would refuse (NaN or infinite float, a time outside
// years 0 to 9999), which readBuckets reports as the same byte-limit error.
//
// Keys are counted with their quotes, colon and a comma. Omitempty fields cost
// nothing at zero. A float is bounded at 24 bytes (-1.7976931348623157e+308), a
// string at one byte per safe ASCII byte and six (\u00XX) for any other byte.
func estimateBucketJSON(b audit.Bucket) (size int64, ok bool) {
	const (
		braces     = 2
		timeBound  = 32 // "2006-01-02T15:04:05.999999999Z"
		floatBound = 24
	)
	if year := b.At.Year(); year < 0 || year > 9999 {
		return 0, false
	}
	size = braces +
		int64(len(`"sourceId":`)+len(`"controller":`)+len(`"action":`)+len(`"kind":`)+len(`"outcome":`)+len(`"state":`)+
			len(`"at":`)+len(`"bin":`)+len(`"count":`)+len(`"sumMs":`)+len(`"minMs":`)+len(`"maxMs":`)+12) +
		jsonStringBound(b.SourceID) + jsonStringBound(b.Controller) + jsonStringBound(b.Action) +
		jsonStringBound(b.Kind) + jsonStringBound(b.Outcome) + jsonStringBound(b.State) +
		timeBound + decimalLen(int64(b.Bin)) + decimalLen(b.Count)
	for _, v := range [...]float64{b.SumMs, b.MinMs, b.MaxMs} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		if v == 0 {
			size += 2 // "0" or "-0"
		} else {
			size += floatBound
		}
	}
	for _, f := range [...]struct {
		key string
		v   float64
	}{{`"sumAuditMs":`, b.SumAuditMs}, {`"sumApiMs":`, b.SumApiMs}, {`"sumPayloadMs":`, b.SumPayloadMs}} {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) {
			return 0, false
		}
		if f.v != 0 {
			size += int64(len(f.key)) + 1 + floatBound
		}
	}
	for _, c := range [...]struct {
		key string
		v   int64
	}{
		{`"nAudit":`, b.NAudit}, {`"nApi":`, b.NApi}, {`"nPayload":`, b.NPayload},
		{`"pendingAsync":`, b.PendingAsync}, {`"observedPartial":`, b.ObservedPartial},
		{`"count4xx":`, b.Count4xx}, {`"count5xx":`, b.Count5xx}, {`"countHangfireFailed":`, b.CountHangfireFailed},
	} {
		if c.v != 0 {
			size += int64(len(c.key)) + 1 + decimalLen(c.v)
		}
	}
	return size, true
}

// jsonStringBound bounds the encoded length of s, quotes included.
func jsonStringBound(s string) int64 {
	n := int64(2)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x80 || c < 0x20 || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&':
			n += 6
		default:
			n++
		}
	}
	return n
}

// decimalLen is the length of v in base ten, sign included.
func decimalLen(v int64) int64 {
	n := int64(1)
	u := uint64(v)
	if v < 0 {
		n++
		u = -u
	}
	for u >= 10 {
		u /= 10
		n++
	}
	return n
}

func acquire(ctx context.Context, sem chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-sem
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func failedSource(p *profile, message string) sourceAnalysis {
	return sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, ErrorCode: "source_unavailable", Error: message}}
}

func (s *Store) readBuckets(ctx context.Context, p *profile, q audit.Query, start, end time.Time, hint *audit.IDMargin, total, totalBytes, legacyRecords, legacyBytes *atomic.Int64) ([]audit.Bucket, error) {
	q = q.Normalized()
	pipeline := buildSummaryPipeline(q, start, end, !start.Equal(q.BaselineStart) || !end.Equal(q.BaselineEnd))
	// The hint stage is only ever resolved for a modern source, so the legacy
	// branch below never sees one.
	pipeline = hinted(pipeline, hint, start, end)
	if usesLegacyMongo(p.shared.client) {
		pipeline = legacyMetadataPipeline(pipeline)
		return readLegacyBuckets(ctx, p, q, start, pipeline, total, totalBytes, legacyRecords, legacyBytes)
	}
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cur.Close(closeCtx)
	}()
	result := make([]audit.Bucket, 0, 128)
	for cur.Next(ctx) {
		var doc bucketDocument
		if err := cur.Decode(&doc); err != nil {
			return nil, errors.New("could not decode aggregation result")
		}
		if total.Add(1) > audit.MaxSummaryRows {
			return nil, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrSummaryRowLimit)
		}
		bucket := audit.Bucket{
			SourceID: p.source.ID, Controller: doc.Controller, Action: doc.Action, Kind: doc.Kind,
			Outcome: doc.Outcome, State: doc.State, At: doc.At.UTC(), Bin: doc.Bin,
			Count: doc.Count, SumMs: doc.SumMs, MinMs: doc.MinMs, MaxMs: doc.MaxMs,
			SumAuditMs: doc.SumAuditMs, SumApiMs: doc.SumApiMs, SumPayloadMs: doc.SumPayloadMs,
			NAudit: doc.NAudit, NApi: doc.NApi, NPayload: doc.NPayload,
			PendingAsync: doc.PendingAsync, ObservedPartial: doc.ObservedPartial,
			Count4xx: doc.C4xx, Count5xx: doc.C5xx, CountHangfireFailed: doc.CHangfireFailed,
		}
		size, ok := estimateBucketJSON(bucket)
		if !ok || totalBytes.Add(size+1) > maxAnalysisBytes {
			return nil, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrSummaryBytesLimit)
		}
		result = append(result, bucket)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

var _ mongoCursor = (*mongo.Cursor)(nil)
