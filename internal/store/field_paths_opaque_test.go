package store

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// BL-033 / DATA-14: payloads and header maps leave the database only as a type name.
func TestFieldPathsPipelineSendsOnlyTypeNamesOfOpaqueFields(t *testing.T) {
	sent := fieldPathsPipeline()
	if len(sent) != 5 || sent[0][0].Key != "$match" || sent[3][0].Key != "$addFields" || sent[4][0].Key != "$project" {
		t.Fatalf("pipeline shape: %v", sent)
	}
	types := sent[3][0].Value.(bson.D)[0]
	if types.Key != opaqueTypesField {
		t.Fatalf("$addFields writes %q, want %q", types.Key, opaqueTypesField)
	}
	byField := map[string]bson.D{}
	for _, e := range types.Value.(bson.D) {
		byField[e.Key] = e.Value.(bson.D)
	}
	drop := map[string]any{}
	for _, e := range sent[4][0].Value.(bson.D) {
		drop[e.Key] = e.Value
	}
	for _, field := range []string{"RequestPayload", "ResponsePayload", "RequestHeaders", "ResponseHeaders"} {
		expr, ok := byField[field]
		if !ok || len(expr) != 1 || expr[0].Key != "$type" || expr[0].Value != "$"+field {
			t.Errorf("%s: type expression %v, want {$type: $%s}", field, expr, field)
		}
		if v, ok := drop[field]; !ok || v != 0 {
			t.Errorf("%s is not dropped by the $project: %v", field, drop)
		}
	}
	if len(drop) != 4 {
		t.Errorf("the $project must drop only the opaque fields: %v", drop)
	}
}

// What the database answers after the pipeline: the opaque fields are gone, only their type names remain.
func TestFieldPathsReadsOpaqueTypeNamesAndNeverListsTheHelperField(t *testing.T) {
	doc1 := bson.D{
		{Key: "_id", Value: bson.NewObjectID()},
		{Key: "StatusCode", Value: int32(200)},
		{Key: opaqueTypesField, Value: bson.D{
			{Key: "RequestPayload", Value: "object"},
			{Key: "ResponsePayload", Value: "string"},
			{Key: "RequestHeaders", Value: "object"},
			{Key: "ResponseHeaders", Value: "missing"},
		}},
	}
	doc2 := bson.D{
		{Key: "_id", Value: bson.NewObjectID()},
		{Key: opaqueTypesField, Value: bson.D{
			{Key: "RequestPayload", Value: "null"},
			{Key: "ResponsePayload", Value: "customer-1234@example.com"},
			{Key: "RequestHeaders", Value: int32(5)},
		}},
	}
	out, _ := fieldPathsFor(t, doc1, doc2)
	encoded, _ := json.Marshal(out)
	for _, leaked := range []string{opaqueTypesField, "customer-1234", "missing"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("%q leaked into the answer: %s", leaked, encoded)
		}
	}
	got := pathTypes(out)
	want := map[string]string{
		"RequestPayload":  "null|object",
		"ResponsePayload": "other|string",
		"RequestHeaders":  "object",
		"StatusCode":      "int",
		"_id":             "objectId",
	}
	for path, types := range want {
		if got[path] != types {
			t.Errorf("path %q types = %q, want %q (all: %v)", path, got[path], types, got)
		}
	}
	if _, listed := got["ResponseHeaders"]; listed {
		t.Errorf("a missing opaque field must not be listed: %v", got)
	}
	if len(got) != len(want) {
		t.Errorf("unexpected paths: %v", got)
	}
}

// Defence in depth: header maps that still reach the walker show no key name.
func TestFieldPathsHeaderKeysBecomePlaceholder(t *testing.T) {
	doc := bson.D{
		{Key: "RequestHeaders", Value: bson.D{{Key: "X-Tenant-acme", Value: fieldSecret}, {Key: "Cookie", Value: fieldSecret}}},
		{Key: "Wrapper", Value: bson.D{{Key: "ResponseHeaders", Value: bson.D{{Key: "Set-Cookie", Value: fieldSecret}}}}},
	}
	out, _ := fieldPathsFor(t, doc)
	encoded, _ := json.Marshal(out)
	for _, leaked := range []string{"X-Tenant-acme", "Cookie", fieldSecret} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("header key or value %q leaked: %s", leaked, encoded)
		}
	}
	got := pathTypes(out)
	if got["RequestHeaders.*"] != "string" || got["Wrapper.ResponseHeaders.*"] != "string" {
		t.Fatalf("unexpected paths: %v", got)
	}
}
