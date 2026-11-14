package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const fieldSecret = "sk_live_DISTINCTIVE_SECRET_9f3a"

func fieldPathsFor(t *testing.T, docs ...any) (audit.FieldPaths, mongo.Pipeline) {
	t.Helper()
	s, source, handle := recordStore(t, false)
	var sent mongo.Pipeline
	raws := make([]any, 0, len(docs))
	for _, d := range docs {
		b, err := bson.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		raws = append(raws, bson.Raw(b))
	}
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		sent = p
		return &fakeCursor{docs: raws}, nil
	}
	out, err := s.SampleFieldPaths(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out, sent
}

func pathTypes(out audit.FieldPaths) map[string]string {
	m := map[string]string{}
	for _, p := range out.Paths {
		m[p.Path] = strings.Join(p.Types, "|")
	}
	return m
}

func TestSampleFieldPathsReturnsPathsAndTypesNeverValues(t *testing.T) {
	doc1 := bson.D{
		{Key: "_id", Value: bson.NewObjectID()},
		{Key: "UserAgent", Value: fieldSecret},
		{Key: "RequestPayload", Value: "x"},
		{Key: "StatusCode", Value: int32(200)},
		{Key: "PreActionFilterTotalMilliseconds", Value: 12.5},
		{Key: "PreActionFilterTimings", Value: bson.A{
			bson.D{{Key: "FilterName", Value: fieldSecret}, {Key: "ElapsedMilliseconds", Value: 3.0}},
			bson.D{{Key: "FilterName", Value: "B"}, {Key: "ElapsedMilliseconds", Value: int32(4)}},
		}},
		{Key: "Tags", Value: bson.A{fieldSecret, fieldSecret}},
		{Key: "Nested", Value: bson.D{{Key: "Inner", Value: bson.D{{Key: "Token", Value: fieldSecret}}}}},
	}
	doc2 := bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "StatusCode", Value: "OK"}, {Key: "Extra", Value: nil}}
	out, _ := fieldPathsFor(t, doc1, doc2)
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), fieldSecret) || strings.Contains(string(encoded), "sk_live") {
		t.Fatalf("a value leaked into the answer: %s", encoded)
	}
	got := pathTypes(out)
	want := map[string]string{
		"_id": "objectId", "UserAgent": "string", "StatusCode": "int|string", "Extra": "null",
		"PreActionFilterTotalMilliseconds": "double",
		"PreActionFilterTimings":           "array", "PreActionFilterTimings[]": "object",
		"PreActionFilterTimings[].FilterName":          "string",
		"PreActionFilterTimings[].ElapsedMilliseconds": "double|int",
		"Tags": "array", "Tags[]": "string",
		"Nested": "object", "Nested.Inner": "object", "Nested.Inner.Token": "string",
		"RequestPayload": "string",
	}
	for path, types := range want {
		if got[path] != types {
			t.Errorf("path %q types = %q, want %q", path, got[path], types)
		}
	}
	if out.Sampled != 2 || out.Truncated {
		t.Fatalf("sampled=%d truncated=%v", out.Sampled, out.Truncated)
	}
}

func TestSampleFieldPathsPipelineIsNewestFirstAndRedactsPayloads(t *testing.T) {
	_, sent := fieldPathsFor(t)
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "p", Value: sent}}, false, false)
	text := string(encoded)
	for _, want := range []string{`"$sort"`, `"_id":-1`, `"$limit":5`, `"$addFields"`, `"RequestPayload"`, `"ResponsePayload"`} {
		if !strings.Contains(strings.ReplaceAll(text, " ", ""), want) {
			t.Errorf("pipeline lacks %s: %s", want, text)
		}
	}
	if sent[0][0].Key != "$match" {
		t.Fatalf("first stage must be $match for the legacy adapter, got %s", sent[0][0].Key)
	}
}

func TestSampleFieldPathsCapsDepthAndPathCount(t *testing.T) {
	deep := bson.D{{Key: "a", Value: bson.D{{Key: "b", Value: bson.D{{Key: "c", Value: bson.D{{Key: "d", Value: bson.D{{Key: "e", Value: fieldSecret}}}}}}}}}}
	wide := bson.D{}
	for i := 0; i < 300; i++ {
		wide = append(wide, bson.E{Key: fmt.Sprintf("Field%03d", i), Value: fieldSecret})
	}
	out, _ := fieldPathsFor(t, deep)
	got := pathTypes(out)
	if got["a.b.c.d"] != "object" {
		t.Fatalf("depth 4 path missing: %v", got)
	}
	if _, deeper := got["a.b.c.d.e"]; deeper {
		t.Fatalf("depth 5 path must not be listed: %v", got)
	}
	out, _ = fieldPathsFor(t, wide)
	if len(out.Paths) != audit.MaxFieldPaths || !out.Truncated {
		t.Fatalf("paths=%d truncated=%v, want %d and true", len(out.Paths), out.Truncated, audit.MaxFieldPaths)
	}
}

func TestSampleFieldPathsRejectsBlankSourceAndMapsErrors(t *testing.T) {
	s, source, handle := recordStore(t, false)
	if _, err := s.SampleFieldPaths(context.Background(), ""); err == nil {
		t.Fatal("blank source must be rejected")
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, fmt.Errorf("mongodb://user:%s@host refused", fieldSecret)
	}
	_, err := s.SampleFieldPaths(context.Background(), source.ID)
	if err == nil || strings.Contains(err.Error(), fieldSecret) {
		t.Fatalf("driver detail must be hidden, got %v", err)
	}
}
