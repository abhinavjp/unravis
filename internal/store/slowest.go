package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"math"
	"strconv"
)

// slowFilter carries the slowest-ranking options the legacy reader applies in
// Go: the minimum metric value, inclusive. A nil filter means a plain page.
type slowFilter struct {
	min *float64
}

// slowFingerprint binds a slowest cursor to its query, metric (part of the
// query) and threshold, and keeps it apart from request-list cursors.
func slowFingerprint(q audit.Query, min *float64) string {
	bound := ""
	if min != nil {
		bound = strconv.FormatFloat(*min, 'g', -1, 64)
	}
	return pageFingerprint(q, "slowest:"+bound)
}

func slowCursorMetricValid(metric *float64) bool {
	return metric != nil && !math.IsNaN(*metric) && !math.IsInf(*metric, 0) && *metric >= 0
}

func (s *Store) Slowest(ctx context.Context, input audit.PageInput) (audit.Page, error) {
	if err := audit.ValidateSlowest(input); err != nil {
		return audit.Page{}, err
	}
	return s.requestsMode(ctx, input, false, true)
}
func slowRowBefore(a, b pageRow, metric string) bool {
	av, bv := audit.RequestMetric(a.request, metric), audit.RequestMetric(b.request, metric)
	if av == nil {
		return false
	}
	if bv == nil {
		return true
	}
	if *av != *bv {
		return *av > *bv
	}
	return pageRowBefore(a, b)
}
func slowestPipeline(q audit.Query, source string, after *pageCursor, min *float64, limit int) mongo.Pipeline {
	q = q.Normalized()
	original := requestPipeline(q, [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}, source, nil, limit)
	out := mongo.Pipeline{}
	for _, stage := range original {
		if stage[0].Key != "$sort" && stage[0].Key != "$limit" {
			out = append(out, stage)
		}
	}
	field := "durationMs"
	if q.Metric == "api" {
		field = "apiTimeMs"
	}
	bounds := func(lower float64) bson.D {
		return bson.D{{Key: "$gte", Value: lower}, {Key: "$lt", Value: math.MaxFloat64}}
	}
	// The floor is the higher of the explicit minimum and the range minimum for the
	// ranking metric; the range filter itself is already in the shared post match.
	lower := 0.0
	if floor := audit.EffectiveSlowestMin(q, min); floor != nil && *floor > 0 {
		lower = *floor
	}
	valid := bson.D{{Key: "durationMs", Value: bounds(lower)}}
	if field != "durationMs" {
		valid = bson.D{{Key: "durationMs", Value: bounds(0)}, {Key: field, Value: bounds(lower)}}
	}
	out = append(out, bson.D{{Key: "$match", Value: valid}})
	if after != nil && after.Metric != nil {
		out = append(out, bson.D{{Key: "$match", Value: slowAfterMatch(field, source, after)}})
	}
	out = append(out, bson.D{{Key: "$sort", Value: bson.D{{Key: field, Value: -1}, {Key: "initiatedAt", Value: -1}, {Key: "_id", Value: -1}}}}, bson.D{{Key: "$limit", Value: limit}})
	return out
}

// slowAfterMatch keeps the rows strictly after the cursor in the order metric
// descending, start descending, source ascending, ID descending.
func slowAfterMatch(field, source string, after *pageCursor) bson.D {
	metric := *after.Metric
	branches := bson.A{
		bson.D{{Key: field, Value: bson.D{{Key: "$lt", Value: metric}}}},
		bson.D{{Key: field, Value: metric}, {Key: "initiatedAt", Value: bson.D{{Key: "$lt", Value: after.At}}}},
	}
	if source > after.SourceID {
		branches = append(branches, bson.D{{Key: field, Value: metric}, {Key: "initiatedAt", Value: after.At}})
	} else if source == after.SourceID {
		lastID, _ := bson.ObjectIDFromHex(after.ObjectID)
		branches = append(branches, bson.D{{Key: field, Value: metric}, {Key: "initiatedAt", Value: after.At}, {Key: "_id", Value: bson.D{{Key: "$lt", Value: lastID}}}})
	}
	return bson.D{{Key: "$or", Value: branches}}
}
