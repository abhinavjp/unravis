package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// breakdownSource is one source's share of a breakdown. A modern source answers
// in two aggregations (counters for every value, then histogram bins for the
// values that made the global top); a legacy source is scanned once.
type breakdownSource struct {
	p       *profile
	legacy  bool
	ok      bool
	builder *audit.BreakdownBuilder
	result  audit.SourceResult
}

func (b *breakdownSource) fail(code, message string) {
	b.ok = false
	b.result = audit.SourceResult{SourceID: b.p.source.ID, ErrorCode: code, Error: message}
}

// Breakdown groups the requests a query selects by one audit field. It counts
// the same rows the request drill-down pages, so a row's count equals the number
// of records its filter returns.
func (s *Store) Breakdown(ctx context.Context, input audit.BreakdownInput) (audit.Breakdown, error) {
	if err := audit.ValidateBreakdown(input); err != nil {
		return audit.Breakdown{}, err
	}
	input.Query = input.Query.Normalized()
	q := input.Query
	if q.HasAgeRange() {
		// One clock for every source, rendered as a literal, as Analyze does.
		q.Now = s.clock()
		input.Query.Now = q.Now
	}
	prefix, err := breakdownPrefix(q)
	if err != nil {
		return audit.Breakdown{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	profiles := make([]*profile, 0, len(q.SourceIDs))
	releases := make([]func(), 0, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		p, release, err := s.leaseSource(id)
		if err != nil {
			for _, done := range releases {
				done()
			}
			return audit.Breakdown{}, err
		}
		profiles, releases = append(profiles, p), append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	var legacyRecords, legacyBytes atomic.Int64
	merged := audit.NewBreakdownBuilder(input.Dim, input.Top)
	sources := make([]*breakdownSource, len(profiles))
	for i, p := range profiles {
		src := &breakdownSource{p: p, legacy: usesLegacyMongo(p.shared.client)}
		sources[i] = src
		release, code, message := s.admit(ctx, p)
		if release == nil {
			src.fail(code, message)
			continue
		}
		if src.legacy {
			src.builder, err = readLegacyBreakdown(ctx, p, q, cohort, input, &legacyRecords, &legacyBytes)
		} else {
			src.builder, err = readBreakdownCounts(ctx, p, prefix, input)
		}
		release()
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				return audit.Breakdown{}, err
			}
			code, message := sourceQueryError(ctx, err)
			src.fail(code, message)
			continue
		}
		src.ok = true
		merged.Merge(src.builder)
	}
	pooled := audit.PooledKind(q.Kind)
	keys := merged.TopKeys()
	for _, src := range sources {
		if !src.ok || src.legacy || len(keys) == 0 || pooled {
			continue
		}
		release, code, message := s.admit(ctx, src.p)
		if release == nil {
			src.fail(code, message)
			continue
		}
		err = readBreakdownBins(ctx, src.p, prefix, input, keys, src.builder)
		release()
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				return audit.Breakdown{}, err
			}
			code, message := sourceQueryError(ctx, err)
			src.fail(code, message)
		}
	}
	final := audit.NewBreakdownBuilder(input.Dim, input.Top)
	final.SetValueCap(breakdownValueCap(input.Dim))
	final.SetPooled(pooled)
	results := make([]audit.SourceResult, 0, len(sources))
	for _, src := range sources {
		if src.ok {
			final.Merge(src.builder)
			src.result = audit.SourceResult{SourceID: src.p.source.ID, OK: true, Version: src.p.version, IndexWarning: src.p.indexWarning}
		}
		results = append(results, src.result)
	}
	final.Pin(keys)
	out, err := final.Build()
	if err != nil {
		return audit.Breakdown{}, err
	}
	out.Sources = results
	return out, nil
}

// admit takes the global and per-source query slots. On failure the release
// function is nil and the code and message describe why the source was skipped.
func (s *Store) admit(ctx context.Context, p *profile) (func(), string, string) {
	if err := acquire(ctx, s.global); err != nil {
		code, message := sourceAdmissionError(err)
		return nil, code, message
	}
	s.mu.RLock()
	per := s.perSource[p.source.ID]
	s.mu.RUnlock()
	if per == nil {
		<-s.global
		return nil, "source_unavailable", audit.SourceQueryErrorMessage("source_unavailable")
	}
	if err := acquire(ctx, per); err != nil {
		<-s.global
		code, message := sourceAdmissionError(err)
		return nil, code, message
	}
	return func() {
		<-per
		<-s.global
	}, "", ""
}

// breakdownDimExpr is the value of dim on a raw document, capped like the
// request rows. Status and polling type are trimmed. The user id is a 32-bit
// integer or null, never text.
func breakdownDimExpr(dim string) bson.D {
	trimmed := func(input string) bson.D {
		field := input[1:]
		return bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: cappedStringExpr(input, field)}}}}
	}
	switch dim {
	case audit.BreakdownStatusCode:
		return trimmed("$StatusCode")
	case audit.BreakdownPollingType:
		return trimmed("$RequestPollingType")
	case audit.BreakdownUserID:
		return userIDExpr("$UserId")
	case audit.BreakdownUIVersion:
		return cappedStringExpr("$PayrollUIVersion", "PayrollUIVersion")
	case audit.BreakdownUserAgent:
		return cappedStringExpr("$UserAgent", "UserAgent")
	}
	return cappedStringExpr("$UserIp", "UserIp")
}

// breakdownLiteral is keys as a literal list for $in. A "$"-prefixed value is
// text here, never a field path. A user id key is the number it spells, and the
// empty key is null.
func breakdownLiteral(dim string, keys []string) bson.D {
	list := make(bson.A, 0, len(keys))
	for _, key := range keys {
		switch {
		case dim != audit.BreakdownUserID:
			list = append(list, key)
		case key == "":
			list = append(list, nil)
		default:
			if id, err := strconv.Atoi(key); err == nil {
				list = append(list, id)
			}
		}
	}
	return bson.D{{Key: "$literal", Value: list}}
}

// breakdownValueCap is the storage cap, in runes, of the field a dimension reads,
// or zero when the value is never cut (a user id is a number). A value that
// reaches it may have been cut, so its row gets no drill-down filter.
func breakdownValueCap(dim string) int {
	switch dim {
	case audit.BreakdownStatusCode:
		return legacyMetadataStringCaps["StatusCode"]
	case audit.BreakdownUIVersion:
		return legacyMetadataStringCaps["PayrollUIVersion"]
	case audit.BreakdownUserAgent:
		return legacyMetadataStringCaps["UserAgent"]
	case audit.BreakdownUserIP:
		return legacyMetadataStringCaps["UserIp"]
	}
	return 0
}

// breakdownDerivedFields are the derived fields the breakdown's $group and bin
// stages read. They come from the request drill-down pipeline, which is what
// makes a count equal the rows behind it; breakdownPrefix fails if that
// pipeline stops defining one of them.
var breakdownDerivedFields = []string{"_auditMetricDuration", "_auditState", "_auditPending", "_statusClass"}

// breakdownPrefix is the request drill-down pipeline (requestPipelineState with
// no state and no API key) up to its sort: the same time window, record filters
// (recordMatch and recordPostMatch), derived kind, status class, outcome and
// state (auditStateExpr), and the same kind and outcome filters as the summary.
// Sharing it is what makes breakdown counts equal drill-down counts. It adds the
// histogram bin of the metric duration, binned like the summary's.
func breakdownPrefix(q audit.Query) (mongo.Pipeline, error) {
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	var prefix mongo.Pipeline
	for _, stage := range requestPipelineState(q, cohort, "", "breakdown", nil, 1) {
		if stage[0].Key == "$sort" {
			break
		}
		prefix = append(prefix, stage)
	}
	if err := checkBreakdownFields(prefix); err != nil {
		return nil, err
	}
	return append(prefix, bson.D{{Key: "$set", Value: bson.D{{Key: "_auditBin", Value: auditBinExpr("$_auditMetricDuration")}}}}), nil
}

// checkBreakdownFields fails when a $set stage of prefix does not define one of
// breakdownDerivedFields, so a rename in the request pipeline stops the
// breakdown loudly instead of silently grouping on a missing field.
func checkBreakdownFields(prefix mongo.Pipeline) error {
	defined := map[string]bool{}
	for _, stage := range prefix {
		if stage[0].Key != "$set" {
			continue
		}
		if fields, ok := stage[0].Value.(bson.D); ok {
			for _, e := range fields {
				defined[e.Key] = true
			}
		}
	}
	for _, field := range breakdownDerivedFields {
		if !defined[field] {
			return fmt.Errorf("breakdown pipeline: the request pipeline no longer defines %s", field)
		}
	}
	return nil
}

func sumWhen(condition any) bson.D {
	return bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{condition, 1, 0}}}}}
}

func withStages(prefix mongo.Pipeline, stages ...bson.D) mongo.Pipeline {
	out := make(mongo.Pipeline, 0, len(prefix)+len(stages))
	out = append(out, prefix...)
	return append(out, stages...)
}

func rawFloat(v bson.RawValue) (float64, bool) {
	switch v.Type {
	case bson.TypeInt32:
		return float64(v.Int32()), true
	case bson.TypeInt64:
		return float64(v.Int64()), true
	case bson.TypeDouble:
		return v.Double(), true
	}
	return 0, false
}

func rawCount(raw bson.Raw, key string) int64 {
	n, _ := rawFloat(raw.Lookup(key))
	return int64(n)
}

// rawBreakdownKey reads a group value. Text is itself; a user id is its digits;
// null and absent are the empty key.
func rawBreakdownKey(v bson.RawValue) string {
	if v.Type == bson.TypeString {
		return v.StringValue()
	}
	if n, ok := rawFloat(v); ok {
		return strconv.FormatInt(int64(n), 10)
	}
	return ""
}

// readBreakdownCounts runs the first aggregation: one document per distinct
// value with its counters. More than MaxBreakdownGroups values fails closed.
func readBreakdownCounts(ctx context.Context, p *profile, prefix mongo.Pipeline, input audit.BreakdownInput) (*audit.BreakdownBuilder, error) {
	pipeline := withStages(prefix,
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "v", Value: breakdownDimExpr(input.Dim)}}},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "incomplete", Value: sumWhen(bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "incomplete"}}})},
			{Key: "pending", Value: sumWhen("$_auditPending")},
			{Key: "c4xx", Value: statusClassCount(audit.StatusClientError)},
			{Key: "c5xx", Value: statusClassCount(audit.StatusServerError)},
		}}},
		bson.D{{Key: "$limit", Value: audit.MaxBreakdownGroups + 1}},
	)
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewBreakdownBuilder(input.Dim, input.Top)
	groups := 0
	for cur.Next(ctx) {
		groups++
		if groups > audit.MaxBreakdownGroups {
			return nil, fmt.Errorf("%w: %w: more than %d distinct values; narrow the time range or add a filter", audit.ErrLimit, audit.ErrBreakdownGroupLimit, audit.MaxBreakdownGroups)
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, errors.New("decode failure")
		}
		builder.AddCounts(rawBreakdownKey(raw.Lookup("_id", "v")), audit.BreakdownCounts{
			Count: rawCount(raw, "count"), Incomplete: rawCount(raw, "incomplete"), Pending: rawCount(raw, "pending"),
			Count4xx: rawCount(raw, "c4xx"), Count5xx: rawCount(raw, "c5xx"),
		})
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return builder, nil
}

// readBreakdownBins runs the second aggregation: histogram bins of valid rows,
// per listed value and for everything else together. Bounding it to the listed
// values keeps the result near (top+1) x 194 documents however many values exist.
func readBreakdownBins(ctx context.Context, p *profile, prefix mongo.Pipeline, input audit.BreakdownInput, keys []string, builder *audit.BreakdownBuilder) error {
	dim := breakdownDimExpr(input.Dim)
	listed := bson.D{{Key: "$in", Value: bson.A{dim, breakdownLiteral(input.Dim, keys)}}}
	pipeline := withStages(prefix,
		bson.D{{Key: "$match", Value: bson.D{{Key: "_auditState", Value: "valid"}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{
				{Key: "v", Value: bson.D{{Key: "$cond", Value: bson.A{listed, dim, nil}}}},
				{Key: "o", Value: bson.D{{Key: "$cond", Value: bson.A{listed, false, true}}}},
				{Key: "bin", Value: "$_auditBin"},
			}},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "sum", Value: bson.D{{Key: "$sum", Value: "$_auditMetricDuration"}}},
			{Key: "min", Value: bson.D{{Key: "$min", Value: "$_auditMetricDuration"}}},
			{Key: "max", Value: bson.D{{Key: "$max", Value: "$_auditMetricDuration"}}},
		}}},
	)
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return err
	}
	defer closeMongoCursor(cur)
	for cur.Next(ctx) {
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return errors.New("decode failure")
		}
		bin, binOK := rawFloat(raw.Lookup("_id", "bin"))
		sum, sumOK := rawFloat(raw.Lookup("sum"))
		lo, loOK := rawFloat(raw.Lookup("min"))
		hi, hiOK := rawFloat(raw.Lookup("max"))
		if !binOK || !sumOK || !loOK || !hiOK {
			return errors.New("decode failure")
		}
		count := rawCount(raw, "count")
		if other, ok := raw.Lookup("_id", "o").BooleanOK(); ok && other {
			builder.AddOtherBin(int(bin), count, sum, lo, hi)
		} else {
			builder.AddBin(rawBreakdownKey(raw.Lookup("_id", "v")), int(bin), count, sum, lo, hi)
		}
	}
	return cur.Err()
}

// readLegacyBreakdown is the MongoDB 3.4 path. It streams the bounded metadata
// projection of the drill-down pipeline once and classifies in Go with the same
// rules as the legacy request drill-down: the record filters through
// legacyRecordFilterMatches (audit.RecordFilterMatches and audit.StateMatches),
// and the summary's kind and outcome filters through audit.RowInSummary. The
// record carries the query metric's duration and state.
func readLegacyBreakdown(ctx context.Context, p *profile, q audit.Query, cohort [5]string, input audit.BreakdownInput, records, bytes *atomic.Int64) (*audit.BreakdownBuilder, error) {
	pipeline := legacyRowMetadataPipeline(requestPipelineState(q, cohort, "", p.source.ID, nil, 1))
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewBreakdownBuilder(input.Dim, input.Top)
	for cur.Next(ctx) {
		if records.Add(1) > maxLegacyMetadataRecords {
			return nil, legacyScanLimit("record")
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, errors.New("could not decode legacy request metadata")
		}
		if bytes.Add(int64(len(raw))) > maxLegacyMetadataBytes {
			return nil, legacyScanLimit("byte")
		}
		if raw.Lookup("_auditMetadataOversized").Boolean() {
			return nil, legacyScanLimit("metadata field")
		}
		record, _ := normalizeLegacyRecord(raw, q.Metric)
		if record.ID.IsZero() || record.At == nil || record.At.Before(q.Start) || !record.At.Before(q.End) {
			continue
		}
		if !legacyRecordFilterMatches(q, record, record) {
			continue
		}
		if !audit.RowInSummary(cohort[3], cohort[4], record.Kind, record.State, record.Outcome, record.StatusClass) {
			continue
		}
		row := audit.RequestRecord{
			Status: record.Status, UserID: record.UserID, UserIP: record.UserIP, UserAgent: record.UserAgent,
			UIVersion: record.UIVersion, PollingTypeRaw: record.PollingTypeRaw,
		}
		builder.AddRecord(row, record.State, record.PendingAsync, record.StatusClass, record.Duration)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return builder, nil
}