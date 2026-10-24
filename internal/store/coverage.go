package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var _ audit.CoverageProber = (*Store)(nil)

// coverageDocument is one $group result of the census: counts only, never a
// field value. kind and day are the group key.
type coverageDocument struct {
	ID struct {
		Kind string `bson:"kind"`
		Day  string `bson:"day"`
	} `bson:"_id"`
	Docs               int64 `bson:"docs"`
	RequestInitiatedAt int64 `bson:"requestInitiatedAt"`
	CreatedOn          int64 `bson:"createdOn"`
	ApiTime            int64 `bson:"apiTime"`
	AccountID          int64 `bson:"accountId"`
}

// coverageFields are the four census fields: output name and stored field.
var coverageFields = [...][2]string{
	{"requestInitiatedAt", "RequestInitiatedAt"},
	{"createdOn", "CreatedOn"},
	{"apiTime", "ApiTime"},
	{"accountId", "AccountId"},
}

// coveragePipeline counts, per request kind and UTC day, how many documents in
// [start, end) have each census field non-null. The first $match is the time
// $or of buildSummaryPipeline, so the census reads the same window. Nothing is
// projected and the only accumulators are $sum: no field value leaves the server.
func coveragePipeline(start, end time.Time) mongo.Pipeline {
	timingMatch := bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}}
	legacyMatch := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$exists", Value: false}}}}, bson.D{{Key: "RequestInitiatedAt", Value: nil}}}}},
		bson.D{{Key: "CreatedOn", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}},
	}}}
	firstMatch := bson.D{{Key: "$or", Value: bson.A{timingMatch, legacyMatch}}}

	// Copied from buildSummaryPipeline.
	dateOrFallback := func(primary, fallback string) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + primary}}, "date"}}}, "$" + primary,
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fallback}}, "date"}}}, "$" + fallback, nil,
			}}},
		}}}
	}
	polling := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$RequestPollingType", ""}}}}}}}}}
	userAgent := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$UserAgent", ""}}}}}
	kind := bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{polling, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{userAgent, "hangfire"}}}}}}}, {Key: "then", Value: "hangfire"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "synchronous"}}}}, {Key: "then", Value: "synchronous"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "asynchronous"}}}}, {Key: "then", Value: "asynchronous"}},
		}},
		{Key: "default", Value: "unknown"},
	}}}
	day := bson.D{{Key: "$dateToString", Value: bson.D{
		{Key: "format", Value: "%Y-%m-%d"},
		{Key: "date", Value: dateOrFallback("RequestInitiatedAt", "CreatedOn")},
		{Key: "onNull", Value: ""},
	}}}

	group := bson.D{
		{Key: "_id", Value: bson.D{{Key: "kind", Value: kind}, {Key: "day", Value: day}}},
		{Key: "docs", Value: bson.D{{Key: "$sum", Value: 1}}},
	}
	for _, f := range coverageFields {
		group = append(group, bson.E{Key: f[0], Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$" + f[1], nil}}}, 1, 0,
		}}}}}})
	}
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: firstMatch}},
		bson.D{{Key: "$group", Value: group}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id.day", Value: 1}, {Key: "_id.kind", Value: 1}}}},
		// One more than the cap, so an over-cap census is detected, not truncated.
		bson.D{{Key: "$limit", Value: audit.MaxCoverageRows + 1}},
	}
}

func fieldCoverage(nonNull, docs int64) audit.FieldCoverage {
	return audit.FieldCoverage{NonNull: nonNull, Pct: audit.CoveragePct(nonNull, docs)}
}

func coverageRow(kind, day string, docs, ria, created, api, account int64) audit.CoverageRow {
	return audit.CoverageRow{
		Kind: kind, Day: day, Docs: docs,
		RequestInitiatedAt: fieldCoverage(ria, docs), CreatedOn: fieldCoverage(created, docs),
		ApiTime: fieldCoverage(api, docs), AccountID: fieldCoverage(account, docs),
	}
}

// Coverage runs the field-coverage census over a window. It reads every record
// in the window, so it is refused unless the store was started with broad scans
// allowed, and it never runs on the interactive budget.
func (s *Store) Coverage(ctx context.Context, in audit.CoverageInput) (audit.CoverageReport, error) {
	if err := audit.ValidateCoverageInput(in); err != nil {
		return audit.CoverageReport{}, err
	}
	// The gate comes before the lease and before any I/O.
	if !s.allowBroadScans {
		return audit.CoverageReport{}, audit.ErrBroadScanRefused
	}
	p, release, err := s.leaseSource(in.SourceID)
	if err != nil {
		return audit.CoverageReport{}, err
	}
	defer release()
	if usesLegacyMongo(p.shared.client) {
		return audit.CoverageReport{}, fmt.Errorf("%w: coverage census is not available for MongoDB 3.4 sources", audit.ErrInvalidInput)
	}
	opCtx, cancelOp := context.WithTimeout(ctx, queryTimeout)
	defer cancelOp()

	if s.docsExaminedBudget > 0 {
		count, err := s.collectionCount(opCtx, p)
		if err != nil {
			_, message := sourceQueryError(opCtx, err)
			return audit.CoverageReport{}, fmt.Errorf("coverage census could not check the collection size: %s", message)
		}
		if count > s.docsExaminedBudget {
			return audit.CoverageReport{}, fmt.Errorf("%w: the collection holds %d documents, above the docs-examined budget of %d", audit.ErrLimit, count, s.docsExaminedBudget)
		}
	}

	if err := acquire(opCtx, s.global); err != nil {
		return audit.CoverageReport{}, err
	}
	defer func() { <-s.global }()
	s.mu.RLock()
	per := s.perSource[p.source.ID]
	s.mu.RUnlock()
	if per == nil {
		return audit.CoverageReport{}, errSourceNotFound
	}
	if err := acquire(opCtx, per); err != nil {
		return audit.CoverageReport{}, err
	}
	defer func() { <-per }()

	censusCtx := withQueryBudget(opCtx, audit.CensusQueryBudget)
	began := time.Now()
	rows, readErr := s.readCoverage(censusCtx, p, in)
	elapsed := time.Since(began)

	prov := audit.Provenance{
		SourceID: in.SourceID, Window: "coverage", Start: in.Start, End: in.End,
		QueryMode: audit.QueryModeExact,
		ScanClass: audit.ScanClassOf(audit.QueryModeExact, in.End.Sub(in.Start), false),
		ElapsedMs: elapsed.Milliseconds(), MaxTimeMs: audit.CensusQueryBudget.Milliseconds(),
	}
	if readErr != nil {
		if errors.Is(readErr, audit.ErrLimit) {
			return audit.CoverageReport{}, readErr
		}
		code, message := sourceQueryError(censusCtx, readErr)
		if code != "query_timeout" {
			return audit.CoverageReport{}, fmt.Errorf("coverage census failed: %s", message)
		}
		// A timeout keeps whatever was read, labelled partial.
		prov.PartialReason = "timeout"
	}

	var docs, ria, created, api, account int64
	for _, r := range rows {
		docs += r.Docs
		ria += r.RequestInitiatedAt.NonNull
		created += r.CreatedOn.NonNull
		api += r.ApiTime.NonNull
		account += r.AccountID.NonNull
	}
	total := coverageRow("all", "", docs, ria, created, api, account)
	prov.DocsReturned = int64(len(rows))
	prov.SampleSize = docs
	prov.Completeness = audit.CompletenessOf(prov)
	attribution := audit.TenantAttribution(account, docs)
	if prov.PartialReason != "" {
		// A partial census cannot show that accountId is populated enough.
		attribution = audit.TenantAttribution(0, 0)
	}
	return audit.CoverageReport{
		SourceID: in.SourceID, Rows: rows, Total: total,
		TenantAttribution: attribution, Provenance: []audit.Provenance{prov},
	}, nil
}

// collectionCount reads the collection's document count from $collStats. It is
// reached only when a docs-examined budget is set.
func (s *Store) collectionCount(ctx context.Context, p *profile) (int64, error) {
	pipeline := mongo.Pipeline{bson.D{{Key: "$collStats", Value: bson.D{{Key: "count", Value: bson.D{}}}}}}
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return 0, err
	}
	defer closeCursor(cur)
	var count int64
	for cur.Next(ctx) {
		var doc struct {
			Count int64 `bson:"count"`
		}
		if err := cur.Decode(&doc); err != nil {
			return 0, errors.New("could not decode collection statistics")
		}
		count += doc.Count
	}
	if err := cur.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func closeCursor(cur mongoCursor) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cur.Close(ctx)
}

// readCoverage aggregates the census and decodes it. On error it still returns
// the rows read so far.
func (s *Store) readCoverage(ctx context.Context, p *profile, in audit.CoverageInput) ([]audit.CoverageRow, error) {
	rows := []audit.CoverageRow{}
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, coveragePipeline(in.Start, in.End))
	if err != nil {
		return rows, err
	}
	defer closeCursor(cur)
	for cur.Next(ctx) {
		var doc coverageDocument
		if err := cur.Decode(&doc); err != nil {
			return rows, errors.New("could not decode aggregation result")
		}
		if len(rows) >= audit.MaxCoverageRows {
			return nil, fmt.Errorf("%w: the census has more than %d groups", audit.ErrLimit, audit.MaxCoverageRows)
		}
		rows = append(rows, coverageRow(doc.ID.Kind, doc.ID.Day, doc.Docs, doc.RequestInitiatedAt, doc.CreatedOn, doc.ApiTime, doc.AccountID))
	}
	return rows, cur.Err()
}
