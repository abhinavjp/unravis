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

var _ audit.FieldPathSampler = (*Store)(nil)

// maxFieldPathArrayItems bounds how many elements of one array are inspected.
const maxFieldPathArrayItems = 20

// SampleFieldPaths reads the newest audit.FieldPathsSample documents of one
// source and reports the union of their field paths with BSON type names. It is
// read-only and shares the global and per-source query admission limits. No
// field value is ever returned: the two payload fields are replaced by a
// constant on the server, and the walker below keeps only names and types.
func (s *Store) SampleFieldPaths(ctx context.Context, sourceID string) (audit.FieldPaths, error) {
	if sourceID == "" {
		return audit.FieldPaths{}, fmt.Errorf("%w: sourceId is required", audit.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	p, release, err := s.leaseSource(sourceID)
	if err != nil {
		return audit.FieldPaths{}, err
	}
	defer release()
	releaseSlots, denied := s.admit(ctx, p)
	if denied != nil {
		return audit.FieldPaths{}, denied.lookupError()
	}
	defer releaseSlots()

	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, fieldPathsPipeline())
	if err != nil {
		return audit.FieldPaths{}, fieldPathsError(ctx, err)
	}
	defer closeMongoCursor(cur)

	walker := newFieldPathWalker()
	sampled := 0
	for cur.Next(ctx) && sampled < audit.FieldPathsSample {
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return audit.FieldPaths{}, errors.New("could not decode field path sample")
		}
		sampled++
		walker.document("", raw, 1)
	}
	if err := cur.Err(); err != nil {
		return audit.FieldPaths{}, fieldPathsError(ctx, err)
	}
	return walker.result(sampled), nil
}

// fieldPathsPipeline selects the newest documents by _id (ObjectIDs are
// time-ordered). The empty first $match is what the MongoDB 3.4 adapter
// requires of every pipeline. A payload that is text is replaced by the
// constant "x" before it leaves the database: its type stays, its content does
// not travel.
func fieldPathsPipeline() mongo.Pipeline {
	redact := func(field string) bson.E {
		return bson.E{Key: field, Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + field}}, "string"}}}, "x", "$" + field,
		}}}}
	}
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: audit.FieldPathsSample}},
		bson.D{{Key: "$addFields", Value: bson.D{redact("RequestPayload"), redact("ResponsePayload")}}},
	}
}

func fieldPathsError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || mongo.IsTimeout(err) {
		return fmt.Errorf("%w: MongoDB field path sampling timed out", context.DeadlineExceeded)
	}
	return errors.New("MongoDB field path sampling failed; check source availability and read permissions")
}

// fieldPathWalker accumulates path -> set of type names. It never stores a value.
type fieldPathWalker struct {
	types     map[string]map[string]struct{}
	truncated bool
}

func newFieldPathWalker() *fieldPathWalker {
	return &fieldPathWalker{types: map[string]map[string]struct{}{}}
}

func (w *fieldPathWalker) note(path, typeName string) {
	set, known := w.types[path]
	if !known {
		if len(w.types) >= audit.MaxFieldPaths {
			w.truncated = true
			return
		}
		set = map[string]struct{}{}
		w.types[path] = set
	}
	set[typeName] = struct{}{}
}

func fieldPathSegment(name string) string {
	runes := []rune(name)
	if len(runes) > audit.MaxFieldPathName {
		return string(runes[:audit.MaxFieldPathName])
	}
	return name
}

// document records every element of doc under prefix. depth is the depth of
// the elements being listed (1 for the top level).
func (w *fieldPathWalker) document(prefix string, doc bson.Raw, depth int) {
	elements, err := doc.Elements()
	if err != nil {
		return
	}
	for _, element := range elements {
		path := fieldPathSegment(element.Key())
		if prefix != "" {
			path = prefix + "." + path
		}
		w.value(path, element.Value(), depth)
	}
}

func (w *fieldPathWalker) value(path string, value bson.RawValue, depth int) {
	w.note(path, fieldTypeName(value.Type))
	if depth >= audit.MaxFieldPathDepth {
		return
	}
	switch value.Type {
	case bson.TypeEmbeddedDocument:
		if doc, ok := value.DocumentOK(); ok {
			w.document(path, doc, depth+1)
		}
	case bson.TypeArray:
		array, ok := value.ArrayOK()
		if !ok {
			return
		}
		items, err := array.Values()
		if err != nil {
			return
		}
		elementPath := path + "[]"
		for i, item := range items {
			if i >= maxFieldPathArrayItems {
				break
			}
			w.value(elementPath, item, depth+1)
		}
	}
}

func (w *fieldPathWalker) result(sampled int) audit.FieldPaths {
	paths := make([]audit.FieldPath, 0, len(w.types))
	for path, set := range w.types {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		paths = append(paths, audit.FieldPath{Path: path, Types: names})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	return audit.FieldPaths{Sampled: sampled, Paths: paths, Truncated: w.truncated}
}

// fieldTypeName uses the MongoDB $type aliases.
func fieldTypeName(t bson.Type) string {
	switch t {
	case bson.TypeDouble:
		return "double"
	case bson.TypeString:
		return "string"
	case bson.TypeEmbeddedDocument:
		return "object"
	case bson.TypeArray:
		return "array"
	case bson.TypeBinary:
		return "binData"
	case bson.TypeObjectID:
		return "objectId"
	case bson.TypeBoolean:
		return "bool"
	case bson.TypeDateTime:
		return "date"
	case bson.TypeNull:
		return "null"
	case bson.TypeRegex:
		return "regex"
	case bson.TypeInt32:
		return "int"
	case bson.TypeTimestamp:
		return "timestamp"
	case bson.TypeInt64:
		return "long"
	case bson.TypeDecimal128:
		return "decimal"
	}
	return "other"
}
