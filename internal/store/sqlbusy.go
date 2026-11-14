package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// sqlBusyMaxBins is the most steps one window may hold (audit.SQLBusyInput
// validates the same bound). The aggregation asks for one extra document so a
// window that somehow exceeds the bound fails closed instead of returning a
// silently shortened series.
const sqlBusyMaxBins = 120

// sqlBusyKey is the perf role whose stored number this reader sums.
const sqlBusyKey = "_perfSqlMs"

// sqlBusyMongoPath looks up the stored field wired to the database-time role in
// recordPerfFields (record_perf.go). An empty path means the role is not wired,
// so the source does not store the number at all.
func sqlBusyMongoPath() string {
	for _, f := range recordPerfFields {
		if f.key == sqlBusyKey {
			return f.mongoPath
		}
	}
	return ""
}

// sqlBusyStartExpr is the effective start of a row: RequestInitiatedAt when it is
// a date, otherwise CreatedOn when it is a date, otherwise null. It is the same
// additive derivation the request pipeline uses for _auditAt (pipeline.go:114 and
// :182), built only from 3.4-safe expression operators, and it keeps every raw
// field (so the stored database-time number survives into the $group).
func sqlBusyStartExpr() bson.D {
	dateOrFallback := func(primary, fallback string) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + primary}}, "date"}}}, "$" + primary,
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fallback}}, "date"}}}, "$" + fallback, nil,
			}}},
		}}}
	}
	return dateOrFallback("RequestInitiatedAt", "CreatedOn")
}

// sqlBusyStepExpr is the start, in Unix milliseconds, of the UTC step that holds
// ts. It is the rollup's slot expression (rollup.go:170 rollupSlotExpr) with the
// step width and the timestamp field passed in: subtract the epoch, take the
// remainder modulo the step, and subtract that remainder. epoch is time.Unix(0,0)
// UTC, exactly as the rollup uses. Only 3.4-safe operators are used.
func sqlBusyStepExpr(tsField string, stepMs int64) bson.D {
	ms := bson.D{{Key: "$subtract", Value: bson.A{tsField, time.Unix(0, 0).UTC()}}}
	return bson.D{{Key: "$subtract", Value: bson.A{ms, bson.D{{Key: "$mod", Value: bson.A{ms, stepMs}}}}}}
}

// sqlBusyPipeline builds the one bounded, read-only aggregation for a window. It
// is a pure function so its shape can be asserted without a database.
//
// The time match is the rollup's: the index-using raw range on RequestInitiatedAt
// with the CreatedOn fallback (pipeline.go:99 timingMatch and the legacyMatch
// beside it), then the duration-safe range on the derived start (rollup.go:187).
// The numeric guard keeps only rows whose stored number really is a number, so
// the sum and the decode are safe. tsField is the derived start field the group
// reads ("$_auditAt"); the $addFields stage defines it. Every operator here is
// available in MongoDB 3.4, and the pipeline carries none of the request
// pipeline's derived analytics fields, so the unified legacy adapter passes it
// through unchanged (legacy_pipeline.go:67) and one pipeline serves both server
// generations. No write stage and no disk use are requested.
func sqlBusyPipeline(in audit.SQLBusyInput, path, tsField string) mongo.Pipeline {
	stepMs := int64(in.BucketSeconds) * 1000
	field := strings.TrimPrefix(tsField, "$")
	timingMatch := bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$gte", Value: in.Start}, {Key: "$lt", Value: in.End}}}}
	legacyMatch := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "RequestInitiatedAt", Value: nil}},
		}}},
		bson.D{{Key: "CreatedOn", Value: bson.D{{Key: "$gte", Value: in.Start}, {Key: "$lt", Value: in.End}}}},
	}}}
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "$or", Value: bson.A{timingMatch, legacyMatch}},
			{Key: path, Value: bson.D{{Key: "$type", Value: "number"}}},
		}}},
		bson.D{{Key: "$addFields", Value: bson.D{{Key: field, Value: sqlBusyStartExpr()}}}},
		bson.D{{Key: "$match", Value: bson.D{{Key: field, Value: bson.D{{Key: "$gte", Value: in.Start}, {Key: "$lt", Value: in.End}}}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: sqlBusyStepExpr(tsField, stepMs)},
			{Key: "busy", Value: bson.D{{Key: "$sum", Value: "$" + path}}},
			{Key: "calls", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
		bson.D{{Key: "$limit", Value: sqlBusyMaxBins + 1}},
	}
}

// readSQLBusy runs the aggregation on one source and decodes its steps. A step
// whose summed number is negative, NaN or infinite is dropped (the same numeric
// safety perfNumberValue applies, record_perf.go:173). More than sqlBusyMaxBins
// documents fails closed. Errors carry no document content; a timeout is returned
// unchanged for the caller to map.
func readSQLBusy(ctx context.Context, p *profile, pipeline mongo.Pipeline) ([]audit.SQLBusyBin, error) {
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer closeMongoCursor(cur)
	var bins []audit.SQLBusyBin
	groups := 0
	for cur.Next(ctx) {
		groups++
		if groups > sqlBusyMaxBins {
			return nil, fmt.Errorf("%w: too many time steps", audit.ErrLimit)
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, errors.New("decode failure")
		}
		slotMs, ok := rawFloat(raw.Lookup("_id"))
		if !ok {
			return nil, errors.New("decode failure")
		}
		busy := perfNumberValue(raw.Lookup("busy"))
		if busy == nil {
			continue
		}
		bins = append(bins, audit.SQLBusyBin{
			Start:  time.UnixMilli(int64(slotMs)).UTC(),
			BusyMs: *busy,
			Calls:  rawCount(raw, "calls"),
		})
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return bins, nil
}

// SQLBusy sums the stored database time of the calls that started in each step,
// across every connected source. It implements audit.SQLBusyReader.
//
// Source selection: the input carries no source ids, so this reads all connected
// sources in s.sources (the repository merges its sources internally, like
// Rollup). Each source is leased and admitted, runs the one bounded aggregation,
// and its steps are summed by step start across sources.
//
// Error handling: a source that fails, or is refused admission, fails the whole
// call and its error is returned (a timeout unchanged). SQLBusyResult has no
// per-source channel, so skipping a failed source would present an undercounted
// sum as if it were complete; failing closed is the honest answer and gives the
// server lane an error to surface. The recorded path is a schema fact, so
// Recorded is true whenever the role is wired, even when no step matched.
func (s *Store) SQLBusy(ctx context.Context, in audit.SQLBusyInput) (audit.SQLBusyResult, error) {
	if err := in.Validate(); err != nil {
		return audit.SQLBusyResult{}, err
	}
	path := sqlBusyMongoPath()
	if path == "" {
		return audit.SQLBusyResult{Recorded: false}, nil
	}

	s.mu.RLock()
	ids := make([]string, 0, len(s.sources))
	for id := range s.sources {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	sort.Strings(ids)

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	readCtx := withQueryBudget(ctx, audit.InteractiveQueryBudget)

	profiles := make([]*profile, 0, len(ids))
	releases := make([]func(), 0, len(ids))
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for _, id := range ids {
		p, release, err := s.leaseSource(id)
		if err != nil {
			return audit.SQLBusyResult{}, err
		}
		profiles, releases = append(profiles, p), append(releases, release)
	}

	pipeline := sqlBusyPipeline(in, path, "$_auditAt")
	type sourceResult struct {
		bins []audit.SQLBusyBin
		err  error
	}
	results := make([]sourceResult, len(profiles))
	fanOut(len(profiles), func(i int) {
		p := profiles[i]
		release, denied := s.admit(readCtx, p)
		if denied != nil {
			results[i].err = denied.lookupError()
			return
		}
		defer release()
		results[i].bins, results[i].err = readSQLBusy(readCtx, p, pipeline)
	})
	for _, r := range results {
		if r.err != nil {
			return audit.SQLBusyResult{}, r.err
		}
	}
	if err := ctx.Err(); err != nil {
		return audit.SQLBusyResult{}, err
	}

	merged := make(map[int64]audit.SQLBusyBin)
	for _, r := range results {
		for _, b := range r.bins {
			key := b.Start.UnixMilli()
			if existing, ok := merged[key]; ok {
				existing.BusyMs += b.BusyMs
				existing.Calls += b.Calls
				merged[key] = existing
			} else {
				merged[key] = b
			}
		}
	}
	keys := make([]int64, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := audit.SQLBusyResult{Recorded: true, Bins: make([]audit.SQLBusyBin, 0, len(keys))}
	for _, key := range keys {
		out.Bins = append(out.Bins, merged[key])
	}
	return out, nil
}

var _ audit.SQLBusyReader = (*Store)(nil)
