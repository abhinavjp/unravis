package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"math"
)

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
func slowestPipeline(q audit.Query, source string, limit int) mongo.Pipeline {
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
	valid := bson.D{{Key: "durationMs", Value: bson.D{{Key: "$gte", Value: 0}, {Key: "$lt", Value: math.MaxFloat64}}}}
	if field != "durationMs" {
		valid = append(valid, bson.E{Key: field, Value: bson.D{{Key: "$gte", Value: 0}, {Key: "$lt", Value: math.MaxFloat64}}})
	}
	out = append(out, bson.D{{Key: "$match", Value: valid}}, bson.D{{Key: "$sort", Value: bson.D{{Key: field, Value: -1}, {Key: "initiatedAt", Value: -1}, {Key: "_id", Value: -1}}}}, bson.D{{Key: "$limit", Value: limit}})
	return out
}
