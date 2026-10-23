package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestPayloadRejectsMalformedObjectIDBeforeQuery(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	called := false
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		called = true
		return &fakeCursor{}, nil
	}
	if _, err := s.Payload(context.Background(), audit.PayloadInput{SourceID: source.ID, ID: "not-an-object-id"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("malformed id error = %v", err)
	}
	if called {
		t.Fatal("malformed ObjectID reached MongoDB")
	}
}

func TestPayloadBoundsSerializedUTF8AndHandlesInvalidTypes(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	objectID := bson.NewObjectID()
	raw, err := bson.Marshal(bson.D{{Key: "request", Value: strings.Repeat("漢🙂", 100000)}, {Key: "response", Value: strings.Repeat("é", 200000)}})
	if err != nil {
		t.Fatal(err)
	}
	handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
		if strings.Contains(string(encoded), "ControllerName") {
			t.Fatal("payload query included unrelated document fields")
		}
		return &fakeCursor{docs: []any{bson.Raw(raw)}}, nil
	}
	payload, err := s.Payload(context.Background(), audit.PayloadInput{SourceID: source.ID, ID: objectID.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > audit.MaxPayloadBytes || !payload.Truncated {
		t.Fatalf("payload output size=%d truncated=%t", len(encoded), payload.Truncated)
	}
	if !json.Valid(encoded) || !strings.Contains(payload.Request, "🙂") || !strings.Contains(payload.Response, "é") {
		t.Fatal("UTF-8 payload was damaged")
	}

	badType, _ := bson.Marshal(bson.D{{Key: "request", Value: 42}, {Key: "response", Value: bson.D{{Key: "nested", Value: "secret"}}}})
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{bson.Raw(badType)}}, nil
	}
	invalid, err := s.Payload(context.Background(), audit.PayloadInput{SourceID: source.ID, ID: objectID.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if !invalid.Truncated || invalid.Request != "" || invalid.Response != "" {
		t.Fatalf("invalid payload types were not safely omitted: %#v", invalid)
	}
}

func TestPayloadEscapingStillFitsResponseByteLimit(t *testing.T) {
	payload := fitPayload(audit.Payload{Request: strings.Repeat("\n\u0001", 200000), Response: strings.Repeat("x", 200000)})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > audit.MaxPayloadBytes || !payload.Truncated {
		t.Fatalf("escaped payload output size=%d truncated=%t", len(encoded), payload.Truncated)
	}
}
