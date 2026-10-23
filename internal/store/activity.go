package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (s *Store) Activity(ctx context.Context, input audit.ActivityInput) (audit.Page, error) {
	q, err := audit.ActivityQuery(input)
	if err != nil {
		return audit.Page{}, err
	}
	return s.requests(ctx, audit.PageInput{Query: q, Limit: input.Limit, Cursor: input.Cursor}, true)
}
func activityPipeline(q audit.Query, source string, after *pageCursor, limit int) mongo.Pipeline {
	pipeline := requestPipeline(q, [5]string{"", "", "", "all", "all"}, source, after, limit)
	if len(pipeline) == 0 {
		return pipeline
	}
	overlap := bson.M{"$and": bson.A{
		bson.M{"RequestCompletedAt": bson.M{"$gt": q.Start}},
		bson.M{"$or": bson.A{
			bson.M{"RequestInitiatedAt": bson.M{"$lt": q.Start}},
			bson.M{"$and": bson.A{bson.M{"RequestInitiatedAt": nil}, bson.M{"CreatedOn": bson.M{"$lt": q.Start}}}},
		}},
	}}
	existing := pipeline[0][0].Value
	pipeline[0] = bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{existing, overlap}}}}}
	match := bson.D{{Key: "$match", Value: bson.M{"$expr": bson.M{"$and": bson.A{
		bson.M{"$lt": bson.A{"$_auditAt", q.End}},
		bson.M{"$or": bson.A{
			bson.M{"$gte": bson.A{"$_auditAt", q.Start}},
			bson.M{"$and": bson.A{bson.M{"$gt": bson.A{"$_auditCompleted", q.Start}}, bson.M{"$gte": bson.A{"$_auditCompleted", "$_auditAt"}}}},
		}},
	}}}}}
	out := make(mongo.Pipeline, 0, len(pipeline)+1)
	out = append(out, pipeline[:2]...)
	out = append(out, match)
	out = append(out, pipeline[2:]...)
	return out
}
