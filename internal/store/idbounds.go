package store

import (
	"context"
	"errors"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// errHintLegacy is why hint mode is refused on a MongoDB 3.4 source.
var errHintLegacy = errors.New("not available on MongoDB 3.4")

// calibrationPipeline reads the newest IDCalibrationSample rows through the _id
// index and projects only what the offset needs, so it is narrow.
func calibrationPipeline() mongo.Pipeline {
	return mongo.Pipeline{
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: int64(audit.IDCalibrationSample)}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 1}, {Key: "RequestInitiatedAt", Value: 1}}}},
	}
}

// idMargin returns the calibrated _id margin for a source. A value younger than
// IDMarginTTL comes from the cache; otherwise the calibration aggregate runs
// under the interactive budget. The caller holds the source's admission slot.
func (s *Store) idMargin(ctx context.Context, p *profile) (audit.IDMargin, error) {
	if usesLegacyMongo(p.shared.client) {
		return audit.IDMargin{}, errHintLegacy
	}
	id := p.source.ID
	now := s.clock()
	s.marginMu.Lock()
	cached, ok := s.margins[id]
	s.marginMu.Unlock()
	if ok && now.Sub(cached.CalibratedAt) < audit.IDMarginTTL {
		return cached, nil
	}
	runCtx, cancel := commandContext(ctx)
	defer cancel()
	cur, err := p.shared.client.Aggregate(runCtx, p.source.Database, p.source.Collection, calibrationPipeline())
	if err != nil {
		return audit.IDMargin{}, err
	}
	defer closeMongoCursor(cur)
	offsets := make([]time.Duration, 0, audit.IDCalibrationSample)
	var from, to time.Time
	for cur.Next(runCtx) {
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return audit.IDMargin{}, errors.New("could not decode calibration row")
		}
		oid, idOK := raw.Lookup("_id").ObjectIDOK()
		ria, dateOK := raw.Lookup("RequestInitiatedAt").TimeOK()
		if !idOK || !dateOK {
			continue
		}
		ria = ria.UTC()
		offsets = append(offsets, oid.Timestamp().Sub(ria))
		if from.IsZero() || ria.Before(from) {
			from = ria
		}
		if to.IsZero() || ria.After(to) {
			to = ria
		}
	}
	if err := cur.Err(); err != nil {
		return audit.IDMargin{}, err
	}
	observed, margin, err := audit.MarginFromOffsets(offsets, audit.IDMarginPad)
	if err != nil {
		return audit.IDMargin{}, err
	}
	m := audit.IDMargin{
		MarginMs: margin.Milliseconds(), ObservedMs: observed.Milliseconds(), PadMs: audit.IDMarginPad.Milliseconds(),
		Percentile: 99.9, SampleSize: len(offsets), SampleFrom: from, SampleTo: to, CalibratedAt: now,
	}
	m.Label = audit.HintLabel(m)
	s.marginMu.Lock()
	s.margins[id] = m
	s.marginMu.Unlock()
	return m, nil
}

func floorSec(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// idHintStage bounds _id to [floor(start-M), floor(end+M)+1s). ObjectId time has
// whole-second resolution, so both bounds round outward.
func idHintStage(start, end time.Time, margin time.Duration) bson.D {
	lower := bson.NewObjectIDFromTimestamp(floorSec(start.Add(-margin)))
	upper := bson.NewObjectIDFromTimestamp(floorSec(end.Add(margin)).Add(time.Second))
	return bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: lower}, {Key: "$lt", Value: upper}}}}}}
}

// withIDHint prepends stage to a copy of p. The pipeline's own time match stays
// as it was; the two matches are ANDed by the server.
func withIDHint(p mongo.Pipeline, stage bson.D) mongo.Pipeline {
	out := make(mongo.Pipeline, 0, len(p)+1)
	out = append(out, stage)
	return append(out, p...)
}

// resolveHint decides whether a source's query runs in hint mode. For any mode
// but hint it returns nothing. In hint mode it returns the margin, or, when hint
// cannot run, no margin and a note giving the reason alone, with no sentence
// around it (the page words "Hint mode was not available ({why})"): the query
// then runs exact, never silently. unsupported, when set, is a reason the caller
// already knows.
func (s *Store) resolveHint(ctx context.Context, p *profile, mode, unsupported string) (*audit.IDMargin, string) {
	if mode != audit.QueryModeHint {
		return nil, ""
	}
	if unsupported != "" {
		return nil, unsupported
	}
	m, err := s.idMargin(ctx, p)
	if err != nil {
		return nil, hintReason(ctx, err)
	}
	return &m, ""
}

// hintReason words a calibration failure as a short reason, without driver
// detail. The too-few-rows reason is shared with the demo.
func hintReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, errHintLegacy):
		return errHintLegacy.Error()
	case errors.Is(err, audit.ErrTooFewCalibrationRows):
		return audit.HintReason(err)
	}
	code, _ := sourceQueryError(ctx, err)
	switch code {
	case "query_timeout":
		return "calibration timed out"
	case "query_cancelled":
		return "calibration was cancelled"
	case "permission_denied":
		return "calibration was denied by MongoDB"
	case "authentication_failed":
		return "calibration could not authenticate"
	case "query_memory_limit":
		return "calibration ran out of query memory"
	case "connection_failed":
		return "the connection was interrupted during calibration"
	}
	return audit.HintReason(err)
}

// hinted applies a resolved margin to a pipeline over [start, end).
func hinted(p mongo.Pipeline, m *audit.IDMargin, start, end time.Time) mongo.Pipeline {
	if m == nil {
		return p
	}
	return withIDHint(p, idHintStage(start, end, time.Duration(m.MarginMs)*time.Millisecond))
}
