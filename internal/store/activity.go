package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"strings"
)

func (s *Store) Activity(ctx context.Context, input audit.ActivityInput) (audit.Page, error) {
	q, err := audit.ActivityQuery(input)
	if err != nil {
		return audit.Page{}, err
	}
	// ActivityQuery leaves the user and API filters off the Query. They ride on it
	// from here, so a cursor is tied to them (the fingerprint covers the Query) and
	// the legacy reader rechecks them in Go. activityPipeline applies them on the
	// server in a stage of their own, never inside the overlap $or.
	if input.UserID != nil {
		q.UserIDs = []int{*input.UserID}
	}
	if input.Operation != "" {
		// Defence in depth: ActivityQuery already refuses anything but one dot
		// with a name on each side. The value is never echoed.
		controller, action, ok := strings.Cut(input.Operation, ".")
		if !ok || controller == "" || action == "" || strings.Contains(action, ".") {
			return audit.Page{}, fmt.Errorf("%w: operation must be written Controller.Action", audit.ErrInvalidInput)
		}
		q.Operation = input.Operation
	}
	return s.requests(ctx, audit.PageInput{Query: q, Limit: input.Limit, Cursor: input.Cursor}, true)
}
func activityPipeline(q audit.Query, source string, after *pageCursor, limit int) mongo.Pipeline {
	// The first $match is built without the user and API filters: they would sit
	// beside the $or below, where the overlap branch lets rows through unchecked.
	bare := q
	bare.UserIDs, bare.Operation, bare.Controller, bare.Action = nil, "", "", ""
	pipeline := requestPipeline(bare, [5]string{"", "", "", "all", "all"}, source, after, limit)
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
	out := make(mongo.Pipeline, 0, len(pipeline)+2)
	out = append(out, pipeline[0])
	if filter := activityFilterMatch(q); len(filter) > 0 {
		out = append(out, bson.D{{Key: "$match", Value: filter}})
	}
	out = append(out, pipeline[1], match)
	out = append(out, pipeline[2:]...)
	return out
}

// activityFilterMatch is the user and API conditions of q as equality tests on
// the raw fields, in a $match that runs on every row after the overlap $or. The
// user uses recordMatch, so an older row that stores the id as text still
// matches. Values are BSON values, never spliced into text. An Operation that
// does not split matches nothing instead of dropping the condition.
func activityFilterMatch(q audit.Query) bson.D {
	q = q.Normalized()
	match := bson.D{}
	if len(q.UserIDs) > 0 {
		match = append(match, recordMatch(audit.Query{UserIDs: q.UserIDs})...)
	}
	if q.Operation != "" {
		return bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}}}
	}
	if q.Controller != "" {
		match = append(match, bson.E{Key: "ControllerName", Value: q.Controller})
	}
	if q.Action != "" {
		match = append(match, bson.E{Key: "ActionName", Value: q.Action})
	}
	return match
}
