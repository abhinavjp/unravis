package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// maxCensusFields bounds distinct field names so documents with data-bearing
// keys cannot grow the census without limit.
const maxCensusFields = 1000

// SampleSchema reports which top-level fields appear in up to n audit
// documents of one source. Only field names, BSON type names and counts leave
// the database; the pipeline never projects a field value. It is read-only and
// shares the global and per-source query admission limits.
func (s *Store) SampleSchema(ctx context.Context, sourceID string, n int) (audit.SchemaCensus, error) {
	if sourceID == "" {
		return audit.SchemaCensus{}, fmt.Errorf("%w: sourceId is required", audit.ErrInvalidInput)
	}
	n = clampSchemaSample(n)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	p, release, err := s.leaseSource(sourceID)
	if err != nil {
		return audit.SchemaCensus{}, err
	}
	defer release()
	if err := acquire(ctx, s.global); err != nil {
		return audit.SchemaCensus{}, fmt.Errorf("%w: query capacity is busy", audit.ErrBusy)
	}
	defer func() { <-s.global }()
	s.mu.RLock()
	per := s.perSource[p.source.ID]
	s.mu.RUnlock()
	if per == nil {
		return audit.SchemaCensus{}, fmt.Errorf("%w: source was removed before the query started", audit.ErrNotFound)
	}
	if err := acquire(ctx, per); err != nil {
		return audit.SchemaCensus{}, fmt.Errorf("%w: source query capacity is busy", audit.ErrBusy)
	}
	defer func() { <-per }()

	pipeline := schemaPipeline(n, usesLegacyMongo(p.shared.client) || isMongo34(p.version))
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return audit.SchemaCensus{}, schemaQueryError(ctx, err)
	}
	defer closeMongoCursor(cur)

	census := audit.SchemaCensus{Fields: []audit.FieldPresence{}}
	present := map[string]int{}
	types := map[string]map[string]struct{}{}
	for cur.Next(ctx) {
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return audit.SchemaCensus{}, errors.New("could not decode schema sample")
		}
		census.Sampled++
		list, ok := raw.Lookup("f").ArrayOK()
		if !ok {
			continue
		}
		entries, err := list.Values()
		if err != nil {
			return audit.SchemaCensus{}, errors.New("could not decode schema sample")
		}
		seen := map[string]struct{}{}
		for _, entry := range entries {
			doc, ok := entry.DocumentOK()
			if !ok {
				continue
			}
			name, nameOK := doc.Lookup("k").StringValueOK()
			typeName, typeOK := doc.Lookup("t").StringValueOK()
			if !nameOK || !typeOK {
				continue
			}
			if _, known := types[name]; !known {
				if len(types) >= maxCensusFields {
					return audit.SchemaCensus{}, fmt.Errorf("%w: sampled documents have more than %d distinct fields", audit.ErrLimit, maxCensusFields)
				}
				types[name] = map[string]struct{}{}
			}
			types[name][typeName] = struct{}{}
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				present[name]++
			}
		}
	}
	if err := cur.Err(); err != nil {
		return audit.SchemaCensus{}, schemaQueryError(ctx, err)
	}
	for name, kinds := range types {
		names := make([]string, 0, len(kinds))
		for kind := range kinds {
			names = append(names, kind)
		}
		sort.Strings(names)
		census.Fields = append(census.Fields, audit.FieldPresence{Name: name, Present: present[name], Types: names})
	}
	sort.Slice(census.Fields, func(i, j int) bool { return census.Fields[i].Name < census.Fields[j].Name })
	return census, nil
}

func clampSchemaSample(n int) int {
	if n <= 0 {
		return audit.DefaultSchemaSample
	}
	return min(n, audit.MaxSchemaSample)
}

// schemaPipeline selects n documents and projects, for each, only the list of
// its top-level field names with their BSON type names. $$e.v is referenced
// only inside $type, so values cannot be returned. MongoDB 3.4 gets a plain
// $limit of the first n documents instead of a random $sample.
func schemaPipeline(n int, legacy bool) mongo.Pipeline {
	pick := bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: int32(n)}}}}
	if legacy {
		pick = bson.D{{Key: "$limit", Value: int32(n)}}
	}
	fields := bson.D{{Key: "$map", Value: bson.D{
		{Key: "input", Value: bson.D{{Key: "$objectToArray", Value: "$$ROOT"}}},
		{Key: "as", Value: "e"},
		{Key: "in", Value: bson.D{
			{Key: "k", Value: "$$e.k"},
			{Key: "t", Value: bson.D{{Key: "$type", Value: "$$e.v"}}},
		}},
	}}}
	return mongo.Pipeline{
		pick,
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 0}, {Key: "f", Value: fields}}}},
	}
}

func schemaQueryError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || mongo.IsTimeout(err) {
		return fmt.Errorf("%w: MongoDB schema sampling timed out", context.DeadlineExceeded)
	}
	return errors.New("MongoDB schema sampling failed; check source availability and read permissions")
}

var _ audit.SchemaSampler = (*Store)(nil)
