package store

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSampleFieldPathsObjectPayloadKeysBecomePlaceholder(t *testing.T) {
	doc := bson.D{
		{Key: "_id", Value: bson.NewObjectID()},
		{Key: "StatusCode", Value: int32(200)},
		{Key: "RequestPayload", Value: bson.D{
			{Key: "customer-1234", Value: bson.D{{Key: "email-9876", Value: fieldSecret}}},
			{Key: "customer-5678", Value: bson.D{{Key: "email-4321", Value: int32(7)}}},
			{Key: "orders", Value: bson.A{bson.D{{Key: "order-77", Value: true}}}},
		}},
		{Key: "ResponsePayload", Value: bson.D{{Key: "acct-42", Value: "text"}}},
	}
	out, _ := fieldPathsFor(t, doc)
	encoded, _ := json.Marshal(out)
	for _, leaked := range []string{"customer-1234", "customer-5678", "email-9876", "email-4321", "order-77", "acct-42", "orders", fieldSecret} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("payload key or value %q leaked into the answer: %s", leaked, encoded)
		}
	}
	got := pathTypes(out)
	want := map[string]string{
		"RequestPayload":       "object",
		"RequestPayload.*":     "array|object",
		"RequestPayload.*.*":   "int|string",
		"RequestPayload.*[]":   "object",
		"ResponsePayload":      "object",
		"ResponsePayload.*":    "string",
		"StatusCode":           "int",
		"_id":                  "objectId",
		"RequestPayload.*[].*": "bool",
	}
	for path, types := range want {
		if got[path] != types {
			t.Errorf("path %q types = %q, want %q (all: %v)", path, got[path], types, got)
		}
	}
	// De-duplicated: two dynamic keys collapse into one placeholder path.
	count := 0
	for _, p := range out.Paths {
		if p.Path == "RequestPayload.*" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("placeholder path listed %d times, want 1", count)
	}
}

func TestSampleFieldPathsPayloadNameAtAnyDepthIsRedacted(t *testing.T) {
	doc := bson.D{
		{Key: "Wrapper", Value: bson.D{{Key: "RequestPayload", Value: bson.D{{Key: "customer-1234", Value: "v"}}}}},
		{Key: "Other", Value: bson.D{{Key: "Structural", Value: "v"}}},
	}
	out, _ := fieldPathsFor(t, doc)
	got := pathTypes(out)
	if _, leaked := got["Wrapper.RequestPayload.customer-1234"]; leaked {
		t.Fatalf("nested payload key leaked: %v", got)
	}
	if got["Wrapper.RequestPayload.*"] != "string" || got["Other.Structural"] != "string" {
		t.Fatalf("unexpected paths: %v", got)
	}
}

func TestSampleFieldPathsNonPayloadObjectKeysUnchanged(t *testing.T) {
	doc := bson.D{
		{Key: "Nested", Value: bson.D{{Key: "Inner", Value: bson.D{{Key: "Token", Value: "v"}}}}},
		{Key: "RequestPayload", Value: "x"},
	}
	out, _ := fieldPathsFor(t, doc)
	got := pathTypes(out)
	if got["Nested.Inner.Token"] != "string" || got["RequestPayload"] != "string" {
		t.Fatalf("top-level behaviour changed: %v", got)
	}
	if _, star := got["RequestPayload.*"]; star {
		t.Fatalf("a text payload has no children: %v", got)
	}
}
