package store

import (
	"context"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// newestPipeline reads the one newest row through the _id index and projects
// only the _id, whose first four bytes are the time the row was created.
func newestPipeline() mongo.Pipeline {
	return mongo.Pipeline{
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: int64(1)}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 1}}}},
	}
}

// Stats reads the size line of one source: the estimated document count from
// $collStats and the time of the newest row from its ObjectID. Neither reads the
// collection, so no broad-scan gate applies. Each half fails soft: a count that
// could not be read is -1, a newest time that could not be read is nil. A source
// that is gone, or an admission refusal, is still an error.
func (s *Store) Stats(ctx context.Context, in audit.StatsInput) (audit.SourceStats, error) {
	p, release, err := s.leaseSource(in.SourceID)
	if err != nil {
		return audit.SourceStats{}, err
	}
	defer release()
	opCtx, cancelOp := context.WithTimeout(ctx, queryTimeout)
	defer cancelOp()
	releaseSlots, denied := s.admit(opCtx, p)
	if denied != nil {
		if denied.Stage == admitRemoved {
			return audit.SourceStats{}, errSourceNotFound
		}
		return audit.SourceStats{}, denied.Err
	}
	defer releaseSlots()

	out := audit.SourceStats{SourceID: in.SourceID, Docs: -1}
	if !usesLegacyMongo(p.shared.client) {
		if n, err := s.collectionCount(opCtx, p); err == nil {
			out.Docs = n
		}
	}
	out.NewestAt = s.newestAt(opCtx, p)
	return out, nil
}

// newestAt returns the creation time of the newest row, or nil.
func (s *Store) newestAt(ctx context.Context, p *profile) *time.Time {
	runCtx, cancel := commandContext(ctx)
	defer cancel()
	cur, err := p.shared.client.Aggregate(runCtx, p.source.Database, p.source.Collection, newestPipeline())
	if err != nil {
		return nil
	}
	defer closeMongoCursor(cur)
	if !cur.Next(runCtx) {
		return nil
	}
	var raw bson.Raw
	if err := cur.Decode(&raw); err != nil {
		return nil
	}
	oid, ok := raw.Lookup("_id").ObjectIDOK()
	if !ok {
		return nil
	}
	t := oid.Timestamp().UTC()
	return &t
}

var _ audit.SourceStatter = (*Store)(nil)
