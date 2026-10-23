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

// TestRecordClassificationMatchesPipelineExpressions checks the Go-side
// classification Store.Record relies on (normalizeLegacyRecord, shared with the
// legacy path) against the server-side pipeline expressions for the shared
// completion fixtures plus a few kind-only documents.
func TestRecordClassificationMatchesPipelineExpressions(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	type fixture struct {
		name, polling, status, userAgent string
		wantState, wantOutcome           string
		checkExpected                    bool
	}
	var fixtures []fixture
	for _, f := range completionFixtures() {
		fixtures = append(fixtures, fixture{name: f.name, polling: f.polling, status: f.status, wantState: f.wantState, wantOutcome: f.wantOutcome, checkExpected: true})
	}
	fixtures = append(fixtures,
		fixture{name: "hangfire by user agent", polling: "Synchronous", status: "OK", userAgent: "Hangfire"},
		fixture{name: "hangfire by polling type", polling: " HANGFIRE ", status: "OK"},
		fixture{name: "no polling type", polling: "", status: "OK"},
		fixture{name: "failure status", polling: "Synchronous", status: "500"},
	)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			doc := completionDoc(f.polling, f.status)
			if f.userAgent != "" {
				doc["UserAgent"] = f.userAgent
			}
			expected := evalSetStages(t, buildSummaryPipeline(q, q.Start, q.End, true), doc)

			id := bson.NewObjectID()
			encoded := map[string]any{"_id": id}
			for k, v := range doc {
				encoded[k] = v
			}
			raw, err := bson.Marshal(encoded)
			if err != nil {
				t.Fatal(err)
			}
			s, source, handle := recordStore(t, false)
			handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
				return &fakeCursor{docs: []any{bson.Raw(raw)}}, nil
			}
			detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
			if err != nil {
				t.Fatal(err)
			}
			if detail.Kind != expected["_auditKind"] || detail.State != expected["_auditState"] || detail.Outcome != expected["_auditOutcome"] {
				t.Fatalf("Go kind/state/outcome = %s/%s/%s, pipeline = %v/%v/%v", detail.Kind, detail.State, detail.Outcome,
					expected["_auditKind"], expected["_auditState"], expected["_auditOutcome"])
			}
			wantDuration, hasDuration := expected["_auditDuration"].(float64)
			if (detail.DurationMs != nil) != hasDuration || (hasDuration && *detail.DurationMs != wantDuration) {
				t.Fatalf("Go observed time = %v, pipeline = %v", detail.DurationMs, expected["_auditDuration"])
			}
			if f.checkExpected && (detail.State != f.wantState || detail.Outcome != f.wantOutcome) {
				t.Fatalf("state/outcome = %s/%s, want %s/%s", detail.State, detail.Outcome, f.wantState, f.wantOutcome)
			}
		})
	}
}

// TestRecordPipelinePassesThroughTheLegacyAdapter proves a MongoDB 3.4 source
// runs the record lookup exactly as written, and that it uses no operator
// introduced after 3.4.
func TestRecordPipelinePassesThroughTheLegacyAdapter(t *testing.T) {
	pipeline := recordPipeline(bson.NewObjectID())
	rewritten, err := rewriteLegacyPipeline(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rewritten, pipeline) {
		t.Fatalf("legacy adapter rewrote the record pipeline:\n got %v\nwant %v", rewritten, pipeline)
	}
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"$convert", "$toString", "$toInt", "$toDouble", "$toDate", "$toLong", "$toObjectId", "$toBool", "$toDecimal", "\"$set\"", "\"$unset\"", "$replaceWith", "$trim", "$ltrim", "$rtrim", "$regexMatch", "$regexFind", "$dateFromString", "$round", "$isNumber"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("record pipeline uses %s, which MongoDB 3.4 does not support", forbidden)
		}
	}
	for _, required := range []string{"$substrCP", "$type", "$cond"} {
		if !strings.Contains(string(encoded), required) {
			t.Errorf("record pipeline lacks %s", required)
		}
	}
}

func TestRecordCapsMetadataStringsInTheProjection(t *testing.T) {
	var project bson.D
	for _, stage := range recordPipeline(bson.NewObjectID()) {
		if stage[0].Key == "$project" {
			project, _ = stage[0].Value.(bson.D)
		}
	}
	for field, want := range map[string]int{"ControllerName": 1024, "ActionName": 1024, "RequestMainUrl": 8192, "UserAgent": 512, "UserIp": 64, "PayrollUIVersion": 128, "UserId": 128, "StatusCode": 128, "RequestPollingType": 128} {
		var value any
		for _, e := range project {
			if e.Key == field {
				value = e.Value
			}
		}
		if !reflect.DeepEqual(value, boundedMetadataValue(field, want, true)) {
			t.Errorf("%s projection = %v, want boundedMetadataValue cap %d", field, value, want)
		}
	}
}

func TestRecordFlagsAbsentOrNonTextPayloads(t *testing.T) {
	// boundedPayloadExpr yields null for a payload that is absent or not a
	// string, so a real MongoDB response carries explicit nulls, not a missing key.
	for name, extra := range map[string][]bson.E{
		"request payload null":  {{Key: "RequestPayload", Value: nil}},
		"response payload null": {{Key: "ResponsePayload", Value: nil}},
		"both null":             {{Key: "RequestPayload", Value: nil}, {Key: "ResponsePayload", Value: nil}},
	} {
		t.Run(name, func(t *testing.T) {
			s, source, handle := recordStore(t, false)
			id := bson.NewObjectID()
			doc := bson.D{{Key: "_id", Value: id}, {Key: "RequestPayload", Value: "{}"}, {Key: "ResponsePayload", Value: "{}"}}
			for _, e := range extra {
				for i := range doc {
					if doc[i].Key == e.Key {
						doc[i].Value = e.Value
					}
				}
			}
			raw, _ := bson.Marshal(doc)
			handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
				return &fakeCursor{docs: []any{bson.Raw(raw)}}, nil
			}
			detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
			if err != nil {
				t.Fatal(err)
			}
			if !detail.Payload.Truncated {
				t.Fatalf("absent or non-text payload was not flagged: %#v", detail.Payload)
			}
		})
	}
}

func TestRecordLookupTimeoutsStayDistinguishable(t *testing.T) {
	s, source, handle := recordStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, context.DeadlineExceeded
	}
	_, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: bson.NewObjectID().Hex()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want context.DeadlineExceeded", err)
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("mongodb://user:secret@host exploded")
	}
	_, err = s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: bson.NewObjectID().Hex()})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("generic failure error = %v", err)
	}
}

func TestLegacyRowMetadataTruncatesEvidenceColumnsWithoutFailingTheScan(t *testing.T) {
	pipeline := rawLegacyMetadataPipelineFor(bson.D{}, legacyRowMetadataFields)
	project, _ := pipeline[len(pipeline)-1][0].Value.(bson.D)
	var oversized any
	for _, e := range project {
		if e.Key == "_auditMetadataOversized" {
			oversized = e.Value
		}
	}
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "x", Value: oversized}}, false, false)
	for _, truncateOnly := range []string{"$UserId", "$UserIp", "$PayrollUIVersion"} {
		if strings.Contains(string(encoded), truncateOnly) {
			t.Errorf("%s participates in the oversized check; a long value would fail the whole scan", truncateOnly)
		}
	}
	if !strings.Contains(string(encoded), "$UserAgent") || !strings.Contains(string(encoded), "$ControllerName") {
		t.Error("existing oversized checks were lost")
	}
	for _, field := range []string{"UserId", "UserIp", "PayrollUIVersion"} {
		var value any
		for _, e := range project {
			if e.Key == field {
				value = e.Value
			}
		}
		if !reflect.DeepEqual(value, boundedMetadataValue(field, legacyMetadataStringCaps[field], true)) {
			t.Errorf("%s is no longer truncated with $substrCP", field)
		}
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
	for key, field := range map[string]string{"userIp": "UserIp", "userAgent": "UserAgent", "uiVersion": "PayrollUIVersion"} {
		for _, e := range project {
			if e.Key == key && !reflect.DeepEqual(e.Value, cappedStringExpr("$"+field, field)) {
				t.Errorf("modern %s projection is not capped with $substrCP: %v", key, e.Value)
			}
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
