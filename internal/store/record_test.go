package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var schemaFieldNames = []string{
	"_id", "ControllerName", "ActionName", "RequestMainUrl", "UserId", "UserIp", "UserAgent", "PayrollUIVersion",
	"RequestPollingType", "StatusCode", "RequestPayload", "ResponsePayload", "RequestLengthInKiloBytes",
	"ResponseLengthInKiloBytes", "RequestInitiatedAt", "CreatedOn", "RequestCompletedAt", "UpdatedOn",
	"AuditCreateTime", "ApiTime", "PayloadRecordTime",
}

type legacyFlagClient struct{ *fakeClient }

func (legacyFlagClient) isLegacy() bool { return true }

func TestRecordProjectionListsOnlySchemaFields(t *testing.T) {
	id := bson.NewObjectID()
	pipeline := recordPipeline(id)
	var project bson.D
	for _, stage := range pipeline {
		switch stage[0].Key {
		case "$project":
			project, _ = stage[0].Value.(bson.D)
		case "$match":
			if !reflect.DeepEqual(stage[0].Value, bson.D{{Key: "_id", Value: id}}) {
				t.Fatalf("record lookup must match exactly one _id, got %v", stage[0].Value)
			}
		case "$limit":
		default:
			t.Fatalf("unexpected stage %s in record pipeline", stage[0].Key)
		}
	}
	got := make([]string, 0, len(project))
	for _, field := range project {
		got = append(got, field.Key)
	}
	want := append([]string(nil), schemaFieldNames...)
	sort.Strings(got)
	sort.Strings(want)
	if len(schemaFieldNames) != 21 || !reflect.DeepEqual(got, want) {
		t.Fatalf("projection keys = %v, want exactly the 21 schema fields %v", got, want)
	}
}

func TestRecordRejectsBadIDAndUnknownSource(t *testing.T) {
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
	if _, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: "not-an-object-id"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("malformed id error = %v", err)
	}
	if _, err := s.Record(context.Background(), audit.RecordInput{SourceID: "", ID: bson.NewObjectID().Hex()}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("empty source error = %v", err)
	}
	if called {
		t.Fatal("rejected input reached MongoDB")
	}
	if _, err := s.Record(context.Background(), audit.RecordInput{SourceID: "no-such-source", ID: bson.NewObjectID().Hex()}); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("unknown source error = %v", err)
	}
	if called {
		t.Fatal("unknown source reached MongoDB")
	}
	if _, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: bson.NewObjectID().Hex()}); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("missing document error = %v", err)
	}
	if !called {
		t.Fatal("well-formed lookup never reached MongoDB")
	}
}

func recordDocument(id bson.ObjectID, extra ...bson.E) bson.Raw {
	initiated := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	created := initiated.Add(40 * time.Millisecond)
	completed := initiated.Add(540 * time.Millisecond)
	updated := completed.Add(30 * time.Millisecond)
	doc := bson.D{
		{Key: "_id", Value: id}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"},
		{Key: "RequestMainUrl", Value: "https://api.example/payroll/run?x=1"}, {Key: "UserId", Value: int32(77)},
		{Key: "UserIp", Value: "203.0.113.9"}, {Key: "UserAgent", Value: "Mozilla/5.0"}, {Key: "PayrollUIVersion", Value: "9.4.1"},
		{Key: "RequestPollingType", Value: "Synchronous "}, {Key: "StatusCode", Value: "OK"},
		{Key: "RequestPayload", Value: "{\"a\":1}"}, {Key: "ResponsePayload", Value: "{\"ok\":true}"},
		{Key: "RequestLengthInKiloBytes", Value: 1.25}, {Key: "ResponseLengthInKiloBytes", Value: 2.5},
		{Key: "RequestInitiatedAt", Value: initiated}, {Key: "CreatedOn", Value: created}, {Key: "RequestCompletedAt", Value: completed},
		{Key: "UpdatedOn", Value: updated}, {Key: "AuditCreateTime", Value: 40.0}, {Key: "ApiTime", Value: 500.0}, {Key: "PayloadRecordTime", Value: 30.0},
	}
	doc = append(doc, extra...)
	raw, err := bson.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

func recordStore(t *testing.T, legacy bool) (*Store, audit.Source, *fakeClient) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		if legacy {
			return legacyFlagClient{handle}, nil
		}
		return handle, nil
	})
	return s, addFakeSource(t, s, "audit"), handle
}

func TestRecordReturnsEverySchemaFieldAndDropsUnknownOnes(t *testing.T) {
	s, source, handle := recordStore(t, false)
	id := bson.NewObjectID()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{recordDocument(id, bson.E{Key: "InternalSecret", Value: "do-not-return"})}}, nil
	}
	detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(detail)
	if strings.Contains(string(encoded), "do-not-return") || strings.Contains(string(encoded), "InternalSecret") {
		t.Fatalf("unknown extra field leaked: %s", encoded)
	}
	if detail.ID != id.Hex() || detail.SourceID != source.ID || detail.Controller != "Payroll" || detail.Action != "Run" ||
		detail.URL != "https://api.example/payroll/run?x=1" || detail.UserID == nil || *detail.UserID != 77 ||
		detail.UserIP != "203.0.113.9" || detail.UserAgent != "Mozilla/5.0" || detail.UIVersion != "9.4.1" ||
		detail.PollingTypeRaw != "Synchronous " || detail.Status != "OK" || detail.RequestKB != 1.25 || detail.ResponseKB != 2.5 {
		t.Fatalf("detail scalar fields wrong: %#v", detail)
	}
	if detail.InitiatedAt == nil || detail.CreatedOn == nil || detail.CompletedAt == nil || detail.UpdatedOn == nil {
		t.Fatalf("detail timestamps missing: %#v", detail)
	}
	if detail.AuditCreateTimeMs == nil || *detail.AuditCreateTimeMs != 40 || detail.ApiTimeMs == nil || *detail.ApiTimeMs != 500 ||
		detail.PayloadRecordTimeMs == nil || *detail.PayloadRecordTimeMs != 30 {
		t.Fatalf("detail phase times wrong: %#v", detail)
	}
	if detail.Kind != "synchronous" || detail.Outcome != "success" || detail.State != "valid" || detail.DurationMs == nil || *detail.DurationMs != 540 {
		t.Fatalf("derived kind/outcome/state/observed wrong: %#v", detail)
	}
	if detail.Payload.Request != "{\"a\":1}" || detail.Payload.Response != "{\"ok\":true}" || detail.Payload.Truncated {
		t.Fatalf("payload wrong: %#v", detail.Payload)
	}
}

func TestRecordPayloadsUseTheExistingCap(t *testing.T) {
	s, source, handle := recordStore(t, false)
	id := bson.NewObjectID()
	huge := bson.E{Key: "RequestPayload", Value: strings.Repeat("漢🙂", 100000)}
	doc, _ := bson.Marshal(bson.D{{Key: "_id", Value: id}, huge, {Key: "ResponsePayload", Value: strings.Repeat("é", 200000)}})
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{bson.Raw(doc)}}, nil
	}
	detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(detail.Payload)
	if len(encoded) > audit.MaxPayloadBytes || !detail.Payload.Truncated || !json.Valid(encoded) {
		t.Fatalf("payload size=%d truncated=%t", len(encoded), detail.Payload.Truncated)
	}
	if detail.State != "incomplete" || detail.Kind != "unknown" {
		t.Fatalf("sparse document derived state=%q kind=%q", detail.State, detail.Kind)
	}
}

func TestLegacyRecordDerivesStateLikeModern(t *testing.T) {
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	completed := created.Add(750 * time.Millisecond)
	cases := []struct {
		name                           string
		doc                            func(bson.ObjectID) bson.Raw
		kind, outcome, state, status   string
		observedMs                     *float64
		pendingAsync, initiatedPresent bool
	}{
		{
			name: "legacy row without RequestInitiatedAt and numeric status",
			doc: func(id bson.ObjectID) bson.Raw {
				raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: id}, {Key: "CreatedOn", Value: created}, {Key: "RequestCompletedAt", Value: completed},
					{Key: "RequestPollingType", Value: "Synchronous "}, {Key: "StatusCode", Value: int32(500)}, {Key: "ApiTime", Value: "750"}})
				return raw
			},
			kind: "synchronous", outcome: "failure", state: "valid", status: "500", observedMs: ptr(750),
		},
		{
			name: "pending async accepted",
			doc: func(id bson.ObjectID) bson.Raw {
				raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: id}, {Key: "RequestInitiatedAt", Value: created}, {Key: "CreatedOn", Value: created},
					{Key: "RequestPollingType", Value: "Asynchronous "}, {Key: "StatusCode", Value: "Accepted"}, {Key: "RequestCompletedAt", Value: completed}})
				return raw
			},
			kind: "asynchronous", outcome: "unknown", state: "incomplete", status: "Accepted", pendingAsync: true, initiatedPresent: true,
		},
		{
			name: "hangfire job whose update never landed",
			doc: func(id bson.ObjectID) bson.Raw {
				raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: id}, {Key: "RequestInitiatedAt", Value: created}, {Key: "CreatedOn", Value: created},
					{Key: "RequestPollingType", Value: "Hangfire"}, {Key: "UserAgent", Value: "Hangfire"}})
				return raw
			},
			kind: "hangfire", outcome: "unknown", state: "incomplete", initiatedPresent: true,
		},
		{
			name: "modern complete row",
			doc:  func(id bson.ObjectID) bson.Raw { return recordDocument(id) },
			kind: "synchronous", outcome: "success", state: "valid", status: "OK", observedMs: ptr(540), initiatedPresent: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := bson.NewObjectID()
			var results [2]audit.RecordDetail
			for i, legacy := range []bool{false, true} {
				s, source, handle := recordStore(t, legacy)
				handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
					return &fakeCursor{docs: []any{tc.doc(id)}}, nil
				}
				detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
				if err != nil {
					t.Fatal(err)
				}
				detail.SourceID = ""
				results[i] = detail
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("legacy and modern details differ:\nmodern=%#v\nlegacy=%#v", results[0], results[1])
			}
			d := results[1]
			if d.Kind != tc.kind || d.Outcome != tc.outcome || d.State != tc.state || d.Status != tc.status || d.PendingAsync != tc.pendingAsync || (d.InitiatedAt != nil) != tc.initiatedPresent {
				t.Fatalf("derived fields = kind %q outcome %q state %q status %q pending %t initiated %v", d.Kind, d.Outcome, d.State, d.Status, d.PendingAsync, d.InitiatedAt)
			}
			if (d.DurationMs == nil) != (tc.observedMs == nil) || (tc.observedMs != nil && *d.DurationMs != *tc.observedMs) {
				t.Fatalf("observed time = %v, want %v", d.DurationMs, tc.observedMs)
			}
		})
	}
}

func ptr(value float64) *float64 { return &value }

func TestRequestRowsCarryAuditEvidenceColumns(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	pipeline := requestPipeline(q, [5]string{}, "s", nil, 101)
	project, _ := pipeline[len(pipeline)-1][0].Value.(bson.D)
	keys := map[string]bool{}
	for _, field := range project {
		keys[field.Key] = true
	}
	for _, key := range []string{"userId", "userIp", "userAgent", "uiVersion", "createdOn", "updatedOn", "pollingTypeRaw"} {
		if !keys[key] {
			t.Errorf("request projection lacks %s", key)
		}
	}
	legacy := legacyRowMetadataPipeline(pipeline)
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: legacy}}, false, false)
	for _, field := range []string{"UserId", "UserIp", "PayrollUIVersion", "UpdatedOn", "CreatedOn", "RequestPollingType"} {
		if !strings.Contains(string(encoded), "\""+field+"\"") {
			t.Errorf("legacy row metadata pipeline lacks %s", field)
		}
	}
	analysis := rawLegacyMetadataPipeline(bson.D{})
	plain, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: analysis}}, false, false)
	if strings.Contains(string(plain), "UserIp") || strings.Contains(string(plain), "PayrollUIVersion") {
		t.Error("analysis scans must not pay for row-only evidence columns")
	}
	id := bson.NewObjectID()
	row, _ := normalizeLegacyRecord(recordDocument(id), "total")
	if row.UserID == nil || *row.UserID != 77 || row.UserIP != "203.0.113.9" || row.UIVersion != "9.4.1" || row.CreatedOn == nil || row.UpdatedOn == nil || row.PollingTypeRaw != "Synchronous " {
		t.Fatalf("normalized row lacks evidence columns: %#v", row)
	}
}
