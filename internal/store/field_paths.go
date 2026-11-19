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
// field value is ever returned: the opaque fields (payloads and header maps)
// leave the database only as their type name, and the walker below keeps only
// names and types.
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
		walker.document("", raw, 1, false)
	}
	if err := cur.Err(); err != nil {
		return audit.FieldPaths{}, fieldPathsError(ctx, err)
	}
	return walker.result(sampled), nil
}

// opaqueFields are the top-level fields whose content is customer data or a
// map whose keys change from call to call (payloads and headers). Their keys
// and values never leave the database: see fieldPathsPipeline.
var opaqueFields = []string{"RequestPayload", "ResponsePayload", "RequestHeaders", "ResponseHeaders"}

// opaqueTypesField holds, per sampled document, the $type name of each opaque
// field. It is added by fieldPathsPipeline and read by the walker; it is never
// listed as a path itself.
const opaqueTypesField = "_unravisOpaqueTypes"

// fieldPathsPipeline selects the newest documents by _id (ObjectIDs are
// time-ordered). The empty first $match is what the MongoDB 3.4 adapter
// requires of every pipeline. Each opaque field is replaced on the database
// server by its type name (a constant such as "object" or "string") and the
// field itself is dropped, so no key name or value of a payload or a header
// map travels. $type and an exclusion $project are both MongoDB 3.4 operators.
func fieldPathsPipeline() mongo.Pipeline {
	types := make(bson.D, 0, len(opaqueFields))
	drop := make(bson.D, 0, len(opaqueFields))
	for _, field := range opaqueFields {
		types = append(types, bson.E{Key: field, Value: bson.D{{Key: "$type", Value: "$" + field}}})
		drop = append(drop, bson.E{Key: field, Value: 0})
	}
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: audit.FieldPathsSample}},
		bson.D{{Key: "$addFields", Value: bson.D{{Key: opaqueTypesField, Value: types}}}},
		bson.D{{Key: "$project", Value: drop}},
	}
}

// mongoTypeNames is the set of $type aliases an opaque type name may take. A
// name outside it is reported as "other", so only a fixed constant is returned.
var mongoTypeNames = map[string]bool{
	"double": true, "string": true, "object": true, "array": true, "binData": true, "undefined": true,
	"objectId": true, "bool": true, "date": true, "null": true, "regex": true, "dbPointer": true,
	"javascript": true, "symbol": true, "javascriptWithScope": true, "int": true, "timestamp": true,
	"long": true, "decimal": true, "minKey": true, "maxKey": true,
}

// opaqueTypes records the type names fieldPathsPipeline put in place of the
// opaque fields. A field the document lacks ($type "missing") is skipped.
func (w *fieldPathWalker) opaqueTypes(doc bson.Raw) {
	for _, field := range opaqueFields {
		value, err := doc.LookupErr(field)
		if err != nil {
			continue
		}
		name, ok := value.StringValueOK()
		if !ok || name == "missing" {
			continue
		}
		if !mongoTypeNames[name] {
			name = "other"
		}
		w.note(field, name)
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

// payloadFieldPlaceholder replaces every key name below an opaque field that
// still reaches the walker (one copied under another parent, or a database
// that did not run the pipeline). Its keys can carry customer values (an id or
// an email used as a key), so they are never returned.
const payloadFieldPlaceholder = "*"

// isPayloadField reports the opaque fields (payloads and header maps). The
// match is by name at any depth, so one copied under another parent is treated
// the same way.
func isPayloadField(name string) bool {
	for _, field := range opaqueFields {
		if name == field {
			return true
		}
	}
	return false
}

// document records every element of doc under prefix. depth is the depth of
// the elements being listed (1 for the top level). inPayload is true below a
// payload field: every key there is replaced by the placeholder segment, and
// repeated placeholders collapse into one path.
func (w *fieldPathWalker) document(prefix string, doc bson.Raw, depth int, inPayload bool) {
	elements, err := doc.Elements()
	if err != nil {
		return
	}
	for _, element := range elements {
		if depth == 1 && element.Key() == opaqueTypesField {
			if types, ok := element.Value().DocumentOK(); ok {
				w.opaqueTypes(types)
			}
			continue
		}
		segment := fieldPathSegment(element.Key())
		childInPayload := inPayload
		if inPayload {
			segment = payloadFieldPlaceholder
		} else if isPayloadField(element.Key()) {
			childInPayload = true
		}
		path := segment
		if prefix != "" {
			path = prefix + "." + segment
		}
		w.value(path, element.Value(), depth, childInPayload)
	}
}

func (w *fieldPathWalker) value(path string, value bson.RawValue, depth int, inPayload bool) {
	w.note(path, fieldTypeName(value.Type))
	if depth >= audit.MaxFieldPathDepth {
		return
	}
	switch value.Type {
	case bson.TypeEmbeddedDocument:
		if doc, ok := value.DocumentOK(); ok {
			w.document(path, doc, depth+1, inPayload)
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
			w.value(elementPath, item, depth+1, inPayload)
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
