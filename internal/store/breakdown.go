package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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
	// code is the error code of a failure (empty when the source read to its end).
	code string
	// Hint mode state, resolved after admission. prefix is the pipeline the reads
	// share, with the _id stage in front when hint ran.
	prefix  mongo.Pipeline
	margin  *audit.IDMargin
	note    string
	ran     bool
	matched int64
	elapsed time.Duration
}

func (b *breakdownSource) fail(code, message string) {
	b.ok = false
	b.code = code
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
	loc, _ := audit.LoadZone(input.TimeZone) // validated above
	merged.SetLocation(loc)
	// Sources are read in parallel, as Analyze reads them: every read goes through
	// admit, so each source still runs one operation at a time and the store four.
	// Each worker writes only its own source and its own slot of limitErrs; results
	// merge afterwards in the order the query listed the sources, so the output does
	// not depend on which source answered first.
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	sources := make([]*breakdownSource, len(profiles))
	limitErrs := make([]error, len(profiles))
	for i, p := range profiles {
		sources[i] = &breakdownSource{p: p, legacy: usesLegacyMongo(p.shared.client)}
	}
	// failLimit records a row-limit failure and stops the sibling reads: the whole
	// breakdown fails closed, as the serial read did at the first such source.
	failLimit := func(i int, err error) {
		limitErrs[i] = err
		cancelWork()
	}
	firstLimit := func() error {
		for _, err := range limitErrs {
			if err != nil {
				return err
			}
		}
		return nil
	}
	fanOut(len(sources), func(i int) {
		src := sources[i]
		p := src.p
		release, denied := s.admit(workCtx, p)
		if denied != nil {
			src.fail(denied.Code, denied.Message)
			return
		}
		// Hint mode: calibrate after admission. The legacy reader streams metadata
		// and never uses it; it runs exact and says why.
		unsupported := ""
		if src.legacy {
			unsupported = errHintLegacy.Error()
		}
		src.margin, src.note = s.resolveHint(workCtx, p, q.QueryMode, unsupported)
		src.prefix = hinted(prefix, src.margin, q.Start, q.End)
		src.ran = true
		began := time.Now()
		var err error
		if src.legacy {
			src.builder, src.matched, err = readLegacyBreakdown(workCtx, p, q, cohort, input, &legacyRecords, &legacyBytes)
		} else {
			src.builder, src.matched, err = readBreakdownCounts(workCtx, p, src.prefix, input)
		}
		src.elapsed = time.Since(began)
		release()
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				failLimit(i, err)
				return
			}
			code, message := sourceQueryError(ctx, err)
			src.fail(code, message)
			return
		}
		src.ok = true
	})
	if err := firstLimit(); err != nil {
		return audit.Breakdown{}, err
	}
	for _, src := range sources {
		if src.ok {
			merged.Merge(src.builder)
		}
	}
	pooled := audit.PooledKind(q.Kind)
	keys := merged.TopKeys()
	fanOut(len(sources), func(i int) {
		src := sources[i]
		if !src.ok || src.legacy || len(keys) == 0 || pooled {
			return
		}
		release, denied := s.admit(workCtx, src.p)
		if denied != nil {
			src.fail(denied.Code, denied.Message)
			return
		}
		began := time.Now()
		err := readBreakdownBins(workCtx, src.p, src.prefix, input, keys, src.builder)
		src.elapsed += time.Since(began)
		release()
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				failLimit(i, err)
				return
			}
			code, message := sourceQueryError(ctx, err)
			src.fail(code, message)
		}
	})
	if err := firstLimit(); err != nil {
		return audit.Breakdown{}, err
	}
	final := audit.NewBreakdownBuilder(input.Dim, input.Top)
	final.SetValueCap(breakdownValueCap(input.Dim))
	final.SetPooled(pooled)
	results := make([]audit.SourceResult, 0, len(sources))
	provenance := make([]audit.Provenance, 0, len(sources))
	for _, src := range sources {
		provenance = append(provenance, src.provenance(q))
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
	out.Provenance = provenance
	return out, nil
}

// fanOut runs fn(0..n-1), one goroutine each, and returns when all have finished.
// Callers keep results in per-index slots so the merge order stays fixed.
func fanOut(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// provenance is the source's entry for the whole breakdown window. A source that
// did not run is a failed exact entry, noting a hint request it never tried; one
// that failed after running keeps the mode it ran in.
func (b *breakdownSource) provenance(q audit.Query) audit.Provenance {
	if !b.ran {
		return failedProvenance(b.p, "current", q.Start, q.End, audit.QueryModeExact, nil, notRunNote(q.QueryMode), b.code, 0)
	}
	mode := audit.QueryModeExact
	if b.margin != nil {
		mode = audit.QueryModeHint
	}
	pv := newProvenance(b.p, "current", q.Start, q.End, mode, b.margin)
	pv.Note = b.note
	finishProvenance(&pv, b.matched, b.matched, b.elapsed, b.code)
	return pv
}

// breakdownDimExpr is the value of dim on a raw document, capped like the
// request rows. Status and polling type are trimmed. The user id is a 32-bit
// integer or null, never text.
func breakdownDimExpr(dim, zone string) bson.D {
	trimmed := func(input string) bson.D {
		field := input[1:]
		return bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: cappedStringExpr(input, field)}}}}
	}
	switch dim {
	case audit.BreakdownHourOfDay:
		if zone == "" {
			zone = "UTC"
		}
		// A row with no usable start time has no hour: the empty key, as in Go.
		return bson.D{{Key: "$ifNull", Value: bson.A{
			bson.D{{Key: "$dateToString", Value: bson.D{{Key: "date", Value: "$_auditAt"}, {Key: "format", Value: "%H:00"}, {Key: "timezone", Value: zone}}}},
			"",
		}}}
	case audit.BreakdownRequestSizeBucket:
		return requestSizeBucketExpr()
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

// requestSizeBucketExpr buckets the stored request size with the same edges as
// audit.SizeBucket. A missing, non-numeric, negative or infinite size is unknown.
// BSON orders NaN below every number, so the negative test catches it too.
func requestSizeBucketExpr() bson.D {
	v := "$$kb"
	lt := func(n float64) bson.D { return bson.D{{Key: "$lt", Value: bson.A{v, n}}} }
	pick := func(condition bson.D, label string) bson.D {
		return bson.D{{Key: "case", Value: condition}, {Key: "then", Value: label}}
	}
	return bson.D{{Key: "$let", Value: bson.D{
		{Key: "vars", Value: bson.D{{Key: "kb", Value: numberExpr("$RequestLengthInKiloBytes")}}},
		{Key: "in", Value: bson.D{{Key: "$switch", Value: bson.D{
			{Key: "branches", Value: bson.A{
				pick(bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: v}}, "null"}}}, audit.SizeBucketUnknown),
				pick(lt(0), audit.SizeBucketUnknown),
				pick(bson.D{{Key: "$gte", Value: bson.A{v, math.Inf(1)}}}, audit.SizeBucketUnknown),
				pick(lt(1), audit.SizeBucketUnder1),
				pick(lt(10), audit.SizeBucket1to10),
				pick(lt(100), audit.SizeBucket10to100),
				pick(lt(1000), audit.SizeBucket100to1000),
			}},
			{Key: "default", Value: audit.SizeBucket1000Plus},
		}}}},
	}}}
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
func readBreakdownCounts(ctx context.Context, p *profile, prefix mongo.Pipeline, input audit.BreakdownInput) (*audit.BreakdownBuilder, int64, error) {
	pipeline := withStages(prefix,
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "v", Value: breakdownDimExpr(input.Dim, input.TimeZone)}}},
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
		return nil, 0, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewBreakdownBuilder(input.Dim, input.Top)
	var matched int64
	groups := 0
	for cur.Next(ctx) {
		groups++
		if groups > audit.MaxBreakdownGroups {
			return nil, 0, fmt.Errorf("%w: %w: more than %d distinct values; narrow the time range or add a filter", audit.ErrLimit, audit.ErrBreakdownGroupLimit, audit.MaxBreakdownGroups)
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, 0, errors.New("decode failure")
		}
		matched += rawCount(raw, "count")
		builder.AddCounts(rawBreakdownKey(raw.Lookup("_id", "v")), audit.BreakdownCounts{
			Count: rawCount(raw, "count"), Incomplete: rawCount(raw, "incomplete"), Pending: rawCount(raw, "pending"),
			Count4xx: rawCount(raw, "c4xx"), Count5xx: rawCount(raw, "c5xx"),
		})
	}
	if err := cur.Err(); err != nil {
		return nil, 0, err
	}
	return builder, matched, nil
}

// readBreakdownBins runs the second aggregation: histogram bins of valid rows,
// per listed value and for everything else together. Bounding it to the listed
// values keeps the result near (top+1) x 194 documents however many values exist.
func readBreakdownBins(ctx context.Context, p *profile, prefix mongo.Pipeline, input audit.BreakdownInput, keys []string, builder *audit.BreakdownBuilder) error {
	dim := breakdownDimExpr(input.Dim, input.TimeZone)
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
func readLegacyBreakdown(ctx context.Context, p *profile, q audit.Query, cohort [5]string, input audit.BreakdownInput, records, bytes *atomic.Int64) (*audit.BreakdownBuilder, int64, error) {
	pipeline := legacyRowMetadataPipeline(requestPipelineState(q, cohort, "", p.source.ID, nil, 1))
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer closeMongoCursor(cur)
	builder := audit.NewBreakdownBuilder(input.Dim, input.Top)
	loc, _ := audit.LoadZone(input.TimeZone) // ValidateBreakdown accepted it
	builder.SetLocation(loc)
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
			InitiatedAt: record.At, RequestKB: record.RequestKB, RequestKBMissing: record.RequestKBValue == nil,
		}
		builder.AddRecord(row, record.State, record.PendingAsync, record.StatusClass, record.Duration)
		matched++
	}
	if err := cur.Err(); err != nil {
		return nil, 0, err
	}
	return builder, matched, nil
}
