package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

type sourceAnalysis struct {
	result    audit.SourceResult
	current   []audit.Bucket
	reference []audit.Bucket
	timings   []audit.SourceTiming
	err       error
}

// Analyze runs current and fixed reference aggregations for each selected source.
func (s *Store) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	if err := audit.ValidateQuery(q); err != nil {
		return audit.Analysis{}, fmt.Errorf("%w: %v", audit.ErrInvalidInput, err)
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
			if err := acquire(workCtx, s.global); err != nil {
				code, message := sourceAdmissionError(err)
				results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, Version: p.version, ErrorCode: code, Error: message}}
				return
			}
			defer func() { <-s.global }()
			s.mu.RLock()
			per := s.perSource[p.source.ID]
			s.mu.RUnlock()
			if per == nil {
				results[i] = failedSource(p, "Source was removed before the query started.")
				return
			}
			if err := acquire(workCtx, per); err != nil {
				code, message := sourceAdmissionError(err)
				results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, Version: p.version, ErrorCode: code, Error: message}}
				return
			}
			defer func() { <-per }()
			var timings []audit.SourceTiming
			timed := func(window string, start, end time.Time) ([]audit.Bucket, error) {
				began := time.Now()
				buckets, err := s.readBuckets(workCtx, p, q, start, end, &rows, &rowBytes, &legacyRecords, &legacyBytes)
				timings = append(timings, audit.SourceTiming{SourceID: p.source.ID, Window: window, ReadMs: time.Since(began).Milliseconds(), Rows: int64(len(buckets))})
				return buckets, err
			}
			current, err := timed("current", q.Start, q.End)
			var reference []audit.Bucket
			if err == nil {
				reference, err = timed("reference", q.BaselineStart, q.BaselineEnd)
			}
			if err == nil {
				results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: true, Version: p.version, IndexWarning: p.indexWarning}, current: current, reference: reference, timings: timings}
				return
			}
			if errors.Is(err, audit.ErrLimit) {
				cancelWork()
				results[i] = sourceAnalysis{err: err}
				return
			}
			code, message := sourceQueryError(queryCtx, err)
			results[i] = sourceAnalysis{result: audit.SourceResult{SourceID: p.source.ID, OK: false, Version: p.version, ErrorCode: code, Error: message}, timings: timings, err: err}
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
	for _, result := range results {
		sourceResults = append(sourceResults, result.result)
		timings = append(timings, result.timings...)
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
	analysis.Warnings = append(analysis.Warnings, "Legacy records without RequestInitiatedAt use CreatedOn when it contains a BSON date.")
	serialized, err := json.Marshal(analysis)
	if err != nil || len(serialized) > maxAnalysisBytes {
		return audit.Analysis{}, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrAnalysisBytesLimit)
	}
	return analysis, nil
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

func (s *Store) readBuckets(ctx context.Context, p *profile, q audit.Query, start, end time.Time, total, totalBytes, legacyRecords, legacyBytes *atomic.Int64) ([]audit.Bucket, error) {
	pipeline := buildSummaryPipeline(q, start, end, !start.Equal(q.BaselineStart) || !end.Equal(q.BaselineEnd))
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
		}
		encoded, err := json.Marshal(bucket)
		if err != nil || totalBytes.Add(int64(len(encoded)+1)) > maxAnalysisBytes {
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
