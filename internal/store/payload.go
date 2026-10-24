package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Payload returns only the selected request and response payload fields.
func (s *Store) Payload(ctx context.Context, input audit.PayloadInput) (audit.Payload, error) {
	if input.SourceID == "" {
		return audit.Payload{}, fmt.Errorf("%w: sourceId is required", audit.ErrInvalidInput)
	}
	objectID, err := bson.ObjectIDFromHex(input.ID)
	if err != nil {
		return audit.Payload{}, fmt.Errorf("%w: request id must be a Mongo ObjectID", audit.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	p, release, err := s.leaseSource(input.SourceID)
	if err != nil {
		return audit.Payload{}, err
	}
	defer release()
	releaseSlots, denied := s.admit(ctx, p)
	if denied != nil {
		return audit.Payload{}, denied.lookupError()
	}
	defer releaseSlots()
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: objectID}}}},
		bson.D{{Key: "$limit", Value: 1}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 0}, {Key: "request", Value: boundedPayloadExpr("$RequestPayload")}, {Key: "response", Value: boundedPayloadExpr("$ResponsePayload")}}}},
	}
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return audit.Payload{}, errors.New("MongoDB payload lookup failed; check source availability and read permissions")
	}
	defer closeMongoCursor(cur)
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return audit.Payload{}, errors.New("MongoDB payload lookup failed; check source availability and read permissions")
		}
		return audit.Payload{}, fmt.Errorf("%w: request was not found", audit.ErrNotFound)
	}
	var raw bson.Raw
	if err := cur.Decode(&raw); err != nil {
		return audit.Payload{}, errors.New("could not decode request payload")
	}
	request, requestOK := raw.Lookup("request").StringValueOK()
	response, responseOK := raw.Lookup("response").StringValueOK()
	payload := audit.Payload{Request: strings.ToValidUTF8(request, "�"), Response: strings.ToValidUTF8(response, "�")}
	if !requestOK || !responseOK {
		payload.Truncated = true
	}
	return fitPayload(payload), nil
}

func boundedPayloadExpr(input string) bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: input}}, "string"}}},
		bson.D{{Key: "$substrCP", Value: bson.A{input, 0, audit.MaxPayloadBytes}}},
		nil,
	}}}
}

func fitPayload(payload audit.Payload) audit.Payload {
	if payloadSize(payload) <= audit.MaxPayloadBytes {
		return payload
	}
	payload.Truncated = true
	originalRequest, originalResponse := payload.Request, payload.Response
	low, high := 0.0, 1.0
	best := audit.Payload{Truncated: true}
	for i := 0; i < 48; i++ {
		ratio := (low + high) / 2
		candidate := audit.Payload{Request: utf8Prefix(originalRequest, int(math.Floor(float64(len(originalRequest))*ratio))), Response: utf8Prefix(originalResponse, int(math.Floor(float64(len(originalResponse))*ratio))), Truncated: true}
		if payloadSize(candidate) <= audit.MaxPayloadBytes {
			best = candidate
			low = ratio
		} else {
			high = ratio
		}
	}
	return best
}

func payloadSize(payload audit.Payload) int {
	serialized, err := json.Marshal(payload)
	if err != nil {
		return audit.MaxPayloadBytes + 1
	}
	return len(serialized)
}

func utf8Prefix(value string, maxBytes int) string {
	if maxBytes >= len(value) {
		return value
	}
	if maxBytes <= 0 {
		return ""
	}
	maxBytes = min(maxBytes, len(value))
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

var _ mongoCursor = (*mongo.Cursor)(nil)
