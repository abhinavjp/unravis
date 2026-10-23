package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// recordSchemaFields lists every documented audit-log field except the two
// payloads, which are projected through boundedPayloadExpr. Anything else on
// the stored document is never requested from MongoDB, so unknown fields cannot
// leak into the detail view.
var recordSchemaFields = []string{
	"ControllerName", "ActionName", "RequestMainUrl", "UserId", "UserIp", "UserAgent", "PayrollUIVersion",
	"RequestPollingType", "StatusCode", "RequestLengthInKiloBytes", "ResponseLengthInKiloBytes",
	"RequestInitiatedAt", "CreatedOn", "RequestCompletedAt", "UpdatedOn", "AuditCreateTime", "ApiTime", "PayloadRecordTime",
}

// recordPipeline fetches one document by _id with an explicit projection of the
// 21 schema fields. It uses only operators available in MongoDB 3.4, so modern
// and legacy sources share it. Metadata strings are capped with $substrCP using
// the same caps and helper as the legacy metadata scan, so one oversized
// UserAgent or URL cannot push the response over its byte limit and hide the
// payloads; values of an unexpected type are returned as null.
func recordPipeline(id bson.ObjectID) mongo.Pipeline {
	projection := bson.D{{Key: "_id", Value: 1}}
	for _, field := range recordSchemaFields {
		stringCap, isString := legacyMetadataStringCaps[field]
		projection = append(projection, bson.E{Key: field, Value: boundedMetadataValue(field, stringCap, isString)})
	}
	projection = append(projection,
		bson.E{Key: "RequestPayload", Value: boundedPayloadExpr("$RequestPayload")},
		bson.E{Key: "ResponsePayload", Value: boundedPayloadExpr("$ResponsePayload")},
	)
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: id}}}},
		bson.D{{Key: "$limit", Value: 1}},
		bson.D{{Key: "$project", Value: projection}},
	}
}

// Record returns the full underlying audit evidence for one record: every
// schema field, the derived kind/state/status class/observed time, and the
// capped payloads. Derived values come from normalizeLegacyRecord, the same
// normalisation the table and analysis use, so the detail view agrees with them.
func (s *Store) Record(ctx context.Context, input audit.RecordInput) (audit.RecordDetail, error) {
	if input.SourceID == "" {
		return audit.RecordDetail{}, fmt.Errorf("%w: sourceId is required", audit.ErrInvalidInput)
	}
	objectID, err := bson.ObjectIDFromHex(input.ID)
	if err != nil {
		return audit.RecordDetail{}, fmt.Errorf("%w: record id must be a Mongo ObjectID", audit.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	p, release, err := s.leaseSource(input.SourceID)
	if err != nil {
		return audit.RecordDetail{}, err
	}
	defer release()
	if err := acquire(ctx, s.global); err != nil {
		return audit.RecordDetail{}, fmt.Errorf("%w: query capacity is busy", audit.ErrBusy)
	}
	defer func() { <-s.global }()
	s.mu.RLock()
	per := s.perSource[p.source.ID]
	s.mu.RUnlock()
	if per == nil {
		return audit.RecordDetail{}, fmt.Errorf("%w: source was removed before the query started", audit.ErrNotFound)
	}
	if err := acquire(ctx, per); err != nil {
		return audit.RecordDetail{}, fmt.Errorf("%w: source query capacity is busy", audit.ErrBusy)
	}
	defer func() { <-per }()
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, recordPipeline(objectID))
	if err != nil {
		return audit.RecordDetail{}, recordLookupError(ctx, err)
	}
	defer closeMongoCursor(cur)
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return audit.RecordDetail{}, recordLookupError(ctx, err)
		}
		return audit.RecordDetail{}, fmt.Errorf("%w: record was not found", audit.ErrNotFound)
	}
	var raw bson.Raw
	if err := cur.Decode(&raw); err != nil {
		return audit.RecordDetail{}, errors.New("could not decode audit record")
	}
	record, _ := normalizeLegacyRecord(raw, "total")
	request, requestOK := raw.Lookup("RequestPayload").StringValueOK()
	response, responseOK := raw.Lookup("ResponsePayload").StringValueOK()
	payload := audit.Payload{Request: strings.ToValidUTF8(request, "�"), Response: strings.ToValidUTF8(response, "�")}
	// Same rule as the payload endpoint: boundedPayloadExpr yields null for a
	// payload that is absent or not text, and that is reported as truncated.
	if !requestOK || !responseOK {
		payload.Truncated = true
	}
	return audit.RecordDetail{
		RequestRecord: audit.RequestRecord{
			ID: objectID.Hex(), SourceID: p.source.ID, Controller: record.Controller, Action: record.Action,
			Kind: record.Kind, Outcome: record.Outcome, Status: record.Status, URL: record.URL,
			InitiatedAt: legacyDate(raw.Lookup("RequestInitiatedAt")), CompletedAt: record.Completed, DurationMs: record.Duration,
			ApiTimeMs: record.API, AuditCreateTimeMs: record.AuditCreate, PayloadRecordTimeMs: record.PayloadRecord,
			RequestKB: record.RequestKB, ResponseKB: record.ResponseKB,
			UserID: record.UserID, UserIP: record.UserIP, UserAgent: record.UserAgent, UIVersion: record.UIVersion,
			CreatedOn: record.CreatedOn, UpdatedOn: record.UpdatedOn, PollingTypeRaw: record.PollingTypeRaw,
		},
		State:        record.State,
		PendingAsync: record.PendingAsync,
		Payload:      fitPayload(payload),
	}, nil
}

// recordLookupError keeps timeouts distinguishable (the server maps
// context.DeadlineExceeded to 504) and hides driver detail for everything else.
func recordLookupError(ctx context.Context, err error) error {
	if code, _ := sourceQueryError(ctx, err); code == "query_timeout" {
		return fmt.Errorf("%w: MongoDB record lookup timed out", context.DeadlineExceeded)
	}
	return errors.New("MongoDB record lookup failed; check source availability and read permissions")
}
