package store

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// rollupSource is one source's share of a rollup.
type rollupSource struct {
	p       *profile
	legacy  bool
	ok      bool
	ran     bool
	builder *audit.RollupBuilder
	result  audit.SourceResult
	code    string
	matched int64
	elapsed time.Duration
}

func (r *rollupSource) fail(code, message string) {
	r.ok = false
	r.code = code
	r.result = audit.SourceResult{SourceID: r.p.source.ID, ErrorCode: code, Error: message}
}

// rollupQuery is the query whose rows a rollup counts: every request of the
// window, of every kind and outcome, with no record filter.
func rollupQuery(plan audit.RollupPlan) audit.Query {
	return audit.Query{
		SourceIDs: plan.SourceIDs, Start: plan.From, End: plan.To,
		BaselineStart: plan.From, BaselineEnd: plan.To,
		Kind: "all", Outcome: "all", Metric: "total", QueryMode: audit.QueryModeExact,
	}.Normalized()
}

// Rollup reads the time slots behind the daily table and the hour grid: one
// aggregation per source, covering the whole window.
//
// A window over audit.LiveRollupMaxWindow reads every request in it, so on a live
// source (every store source is live) it is refused with ErrBroadScanRefused
// unless the store was started with broad scans allowed. The gate runs before any
// lease or I/O. A source that fails, or runs out of its query budget, adds
// nothing: a cut slot list would make every later day wrong. The caller sees it in
// the source results.
func (s *Store) Rollup(ctx context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	broad := plan.To.Sub(plan.From) > audit.LiveRollupMaxWindow
	if broad && !s.allowBroadScans {
		return audit.RollupSlots{}, audit.ErrBroadScanRefused
	}
	q := rollupQuery(plan)
	prefix, err := breakdownPrefix(q)
	if err != nil {
		return audit.RollupSlots{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	profiles := make([]*profile, 0, len(plan.SourceIDs))
	releases := make([]func(), 0, len(plan.SourceIDs))
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for _, id := range plan.SourceIDs {
		p, release, err := s.leaseSource(id)
		if err != nil {
			return audit.RollupSlots{}, err
		}
		profiles, releases = append(profiles, p), append(releases, release)
	}
	budget := audit.InteractiveQueryBudget
	if broad {
		budget = audit.CensusQueryBudget
	}
	readCtx := withQueryBudget(ctx, budget)
	var legacyRecords, legacyBytes atomic.Int64
	workCtx, cancelWork := context.WithCancel(readCtx)
	defer cancelWork()
	sources := make([]*rollupSource, len(profiles))
	limitErrs := make([]error, len(profiles))
	for i, p := range profiles {
		sources[i] = &rollupSource{p: p, legacy: usesLegacyMongo(p.shared.client)}
	}
	fanOut(len(sources), func(i int) {
		src := sources[i]
		release, denied := s.admit(workCtx, src.p)
		if denied != nil {
			src.fail(denied.Code, denied.Message)
			return
		}
		defer release()
		// The size check is a source query too, so it runs under admission.
		if broad && s.docsExaminedBudget > 0 {
			count, err := s.collectionCount(workCtx, src.p)
			if err != nil {
				_, message := sourceQueryError(workCtx, err)
				limitErrs[i] = fmt.Errorf("%w: %s", audit.ErrCollectionSizeCheck, message)
				cancelWork()
				return
			}
			if count > s.docsExaminedBudget {
				limitErrs[i] = fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrDocsExaminedBudget)
				cancelWork()
				return
			}
		}
		src.ran = true
		began := time.Now()
		var err error
		if src.legacy {
			src.builder, src.matched, err = readLegacyRollup(workCtx, src.p, q, plan, &legacyRecords, &legacyBytes)
		} else {
			src.builder, src.matched, err = readRollupSlots(workCtx, src.p, prefix, plan)
		}
		src.elapsed = time.Since(began)
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				limitErrs[i] = err
				cancelWork()
				return
			}
			code, message := sourceQueryError(readCtx, err)
			src.fail(code, message)
			return
		}
		src.ok = true
	})
	for _, err := range limitErrs {
		if err != nil {
			return audit.RollupSlots{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return audit.RollupSlots{}, err
	}
	out := audit.RollupSlots{Builder: audit.NewRollupBuilder(plan.Width)}
	for _, src := range sources {
		if src.ok {
			out.Builder.Merge(src.builder)
			src.result = audit.SourceResult{SourceID: src.p.source.ID, OK: true, Version: src.p.version, IndexWarning: src.p.indexWarning}
		}
		out.Sources = append(out.Sources, src.result)
		out.Provenance = append(out.Provenance, src.provenance(plan, budget))
	}
	return out, nil
}

func (r *rollupSource) provenance(plan audit.RollupPlan, budget time.Duration) audit.Provenance {
	var pv audit.Provenance
	if !r.ran {
		pv = failedProvenance(r.p, "rollup", plan.From, plan.To, audit.QueryModeExact, nil, "", r.code, 0)
	} else {
		pv = newProvenance(r.p, "rollup", plan.From, plan.To, audit.QueryModeExact, nil)
		finishProvenance(&pv, r.matched, r.matched, r.elapsed, r.code)
	}
	pv.MaxTimeMs = budget.Milliseconds()
	return pv
}

// rollupSlotExpr is the start, in Unix milliseconds, of the UTC slot that holds
// the request's start time.
func rollupSlotExpr(width time.Duration) bson.D {
	ms := bson.D{{Key: "$subtract", Value: bson.A{"$_auditAt", time.Unix(0, 0).UTC()}}}
	return bson.D{{Key: "$subtract", Value: bson.A{ms, bson.D{{Key: "$mod", Value: bson.A{ms, width.Milliseconds()}}}}}}
}

// readRollupSlots runs the one aggregation of a modern source: one document per
// (time slot, latency bin). Rows of the latency sample (valid, synchronous) carry
// their bin; every other row falls in the slot's bin-less document, which holds
// counters only. More than audit.MaxRollupGroups documents fails closed.
func readRollupSlots(ctx context.Context, p *profile, prefix mongo.Pipeline, plan audit.RollupPlan) (*audit.RollupBuilder, int64, error) {
	isSync := bson.D{{Key: "$eq", Value: bson.A{"$_auditKind", "synchronous"}}}
	isSample := bson.D{{Key: "$and", Value: bson.A{isSync, bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}}}}}
	isSlow := bson.D{{Key: "$and", Value: bson.A{isSample,
		bson.D{{Key: "$gte", Value: bson.A{"$_auditBin", 0}}},
		bson.D{{Key: "$gte", Value: bson.A{"$_auditMetricDuration", plan.SlowMs}}},
	}}}
	pipeline := withStages(prefix,
		bson.D{{Key: "$match", Value: bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$gte", Value: plan.From}, {Key: "$lt", Value: plan.To}}}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{
				{Key: "s", Value: rollupSlotExpr(plan.Width)},
				{Key: "b", Value: bson.D{{Key: "$cond", Value: bson.A{isSample, "$_auditBin", nil}}}},
			}},
			{Key: "total", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "sync", Value: sumWhen(isSync)},
			{Key: "bg", Value: sumWhen(bson.D{{Key: "$eq", Value: bson.A{"$_auditKind", "hangfire"}}})},
			{Key: "c4xx", Value: statusClassCount(audit.StatusClientError)},
			{Key: "c5xx", Value: statusClassCount(audit.StatusServerError)},
			{Key: "slow", Value: sumWhen(isSlow)},
			{Key: "sum", Value: bson.D{{Key: "$sum", Value: "$_auditMetricDuration"}}},
			{Key: "min", Value: bson.D{{Key: "$min", Value: "$_auditMetricDuration"}}},
			{Key: "max", Value: bson.D{{Key: "$max", Value: "$_auditMetricDuration"}}},
		}}},
		bson.D{{Key: "$limit", Value: audit.MaxRollupGroups + 1}},
	)
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewRollupBuilder(plan.Width)
	var matched int64
	groups := 0
	for cur.Next(ctx) {
		groups++
		if groups > audit.MaxRollupGroups {
			return nil, 0, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrRollupGroupLimit)
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, 0, errors.New("decode failure")
		}
		slotMs, ok := rawFloat(raw.Lookup("_id", "s"))
		if !ok {
			return nil, 0, errors.New("decode failure")
		}
		unix := int64(slotMs) / 1000
		counts := audit.RollupCounts{
			Total: rawCount(raw, "total"), Sync: rawCount(raw, "sync"), Background: rawCount(raw, "bg"),
			C4xx: rawCount(raw, "c4xx"), C5xx: rawCount(raw, "c5xx"), Slow: rawCount(raw, "slow"),
		}
		matched += counts.Total
		builder.AddCounts(unix, counts)
		if bin, binOK := rawFloat(raw.Lookup("_id", "b")); binOK {
			sum, sumOK := rawFloat(raw.Lookup("sum"))
			lo, loOK := rawFloat(raw.Lookup("min"))
			hi, hiOK := rawFloat(raw.Lookup("max"))
			if !sumOK || !loOK || !hiOK {
				return nil, 0, errors.New("decode failure")
			}
			builder.AddBin(unix, int(bin), counts.Total, sum, lo, hi)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, 0, err
	}
	return builder, matched, nil
}

// readLegacyRollup is the MongoDB 3.4 path: it streams the bounded metadata of
// every request in the window and slots them in Go, under the same record and
// byte limits as the other legacy readers.
func readLegacyRollup(ctx context.Context, p *profile, q audit.Query, plan audit.RollupPlan, records, bytes *atomic.Int64) (*audit.RollupBuilder, int64, error) {
	cohort := [5]string{"", "", "", q.Kind, q.Outcome}
	pipeline := legacyRowMetadataPipeline(requestPipelineState(q, cohort, "", p.source.ID, nil, 1))
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewRollupBuilder(plan.Width)
	var matched int64
	for cur.Next(ctx) {
		if records.Add(1) > maxLegacyMetadataRecords {
			return nil, 0, legacyScanLimit("record")
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, 0, errors.New("could not decode legacy request metadata")
		}
		if bytes.Add(int64(len(raw))) > maxLegacyMetadataBytes {
			return nil, 0, legacyScanLimit("byte")
		}
		if raw.Lookup("_auditMetadataOversized").Boolean() {
			return nil, 0, legacyScanLimit("metadata field")
		}
		record, _ := normalizeLegacyRecord(raw, q.Metric)
		if record.ID.IsZero() || record.At == nil || record.At.Before(plan.From) || !record.At.Before(plan.To) {
			continue
		}
		builder.AddRow(*record.At, record.Kind, record.State, record.StatusClass, record.Duration, plan.SlowMs)
		matched++
	}
	if err := cur.Err(); err != nil {
		return nil, 0, err
	}
	return builder, matched, nil
}
