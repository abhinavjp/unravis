package store

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestRewriteLegacyPipelineUsesBoundedMetadataOnlyProjection(t *testing.T) {
	q := audit.Query{BucketSeconds: 30, Metric: "api", Kind: "all", Outcome: "all"}
	input := buildPipeline(q, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	got, err := rewriteLegacyPipeline(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: got}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{"$match", "$sort", "$limit", "$project", "RequestPollingType", "ApiTime", "_auditMetadataOversized", "$substrCP", "$strLenCP", "8192", "128"} {
		if !strings.Contains(text, want) {
			t.Errorf("rewritten pipeline missing %q", want)
		}
	}
	for _, forbidden := range []string{"$convert", "$trim", "$toLong", "$toDate", "$switch", "RequestPayload", "ResponsePayload"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("rewritten pipeline contains unsupported or payload field %q", forbidden)
		}
	}
	if !strings.Contains(text, `100001`) {
		t.Errorf("rewritten pipeline lacks scan sentinel: %s", text)
	}
	if _, err := rewriteLegacyPipeline(got); err != nil {
		t.Fatalf("rewrite is not idempotent: %v", err)
	}
}

func TestRewriteLegacyPipelineLeavesBoundedPayloadPipelineCompatible(t *testing.T) {
	input := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: bson.NewObjectID()}}}},
		bson.D{{Key: "$limit", Value: 1}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "request", Value: boundedPayloadExpr("$RequestPayload")}, {Key: "response", Value: boundedPayloadExpr("$ResponsePayload")}}}},
	}
	got, err := rewriteLegacyPipeline(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(input) {
		t.Fatalf("payload pipeline changed length from %d to %d", len(input), len(got))
	}
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: got}}, false, false)
	if strings.Contains(string(encoded), "$convert") || strings.Contains(string(encoded), "$trim") {
		t.Fatalf("payload pipeline includes unsupported conversion: %s", encoded)
	}
}

func TestNormalizeLegacyRecordPreservesDatesStatusesAndMetricConversions(t *testing.T) {
	at := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	completed := at.Add(2750 * time.Millisecond)
	fields := bson.D{
		{Key: "_id", Value: bson.NewObjectID()}, {Key: "RequestInitiatedAt", Value: at},
		{Key: "RequestCompletedAt", Value: completed}, {Key: "StatusCode", Value: " 201"},
		{Key: "RequestPollingType", Value: "  Asynchronous \t"}, {Key: "ApiTime", Value: "12.75"},
		{Key: "RequestLengthInKiloBytes", Value: "1.5"}, {Key: "ResponseLengthInKiloBytes", Value: int32(2)},
	}
	encoded, err := bson.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := normalizeLegacyRecord(bson.Raw(encoded), "api")
	if record.Kind != "asynchronous" || record.Outcome != "unknown" || record.State != "valid" || record.Duration == nil || *record.Duration != 12.75 {
		t.Fatalf("unexpected normalized record: %#v", record)
	}
	if record.RequestKB != 1.5 || record.ResponseKB != 2 {
		t.Fatalf("string/numeric metrics = %v/%v", record.RequestKB, record.ResponseKB)
	}
	total, _ := normalizeLegacyRecord(bson.Raw(encoded), "total")
	if total.Duration == nil || *total.Duration != 2750 {
		t.Fatalf("date subtraction duration = %v, want 2750ms", total.Duration)
	}
	// A malformed primary timestamp does not use CreatedOn; only missing or null does.
	badDate, _ := bson.Marshal(bson.D{{Key: "RequestInitiatedAt", Value: "bad"}, {Key: "CreatedOn", Value: at}, {Key: "RequestCompletedAt", Value: completed}})
	bad, _ := normalizeLegacyRecord(bson.Raw(badDate), "total")
	if bad.At != nil {
		t.Fatalf("invalid primary date incorrectly fell back: %v", bad.At)
	}
	missingDate, _ := bson.Marshal(bson.D{{Key: "CreatedOn", Value: at}, {Key: "RequestCompletedAt", Value: completed}})
	fallback, _ := normalizeLegacyRecord(bson.Raw(missingDate), "total")
	if fallback.At == nil || !fallback.At.Equal(at) {
		t.Fatalf("missing primary date did not use CreatedOn: %v", fallback.At)
	}
}

func TestLegacyApiMetricRejectsWhitespaceNumericString(t *testing.T) {
	at := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	data, err := bson.Marshal(bson.D{{Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(time.Second)}, {Key: "ApiTime", Value: " 12.5 "}})
	if err != nil {
		t.Fatal(err)
	}
	record, _ := normalizeLegacyRecord(bson.Raw(data), "api")
	if record.State != "invalid" || record.Duration != nil {
		t.Fatalf("whitespace numeric API metric = %#v, want invalid conversion", record)
	}
}

func TestLegacyBucketKeyCannotCollideOnEmbeddedNulls(t *testing.T) {
	one := legacyBucketKey{controller: "A\x00B", action: "C", kind: "unknown", outcome: "unknown", state: "valid", atUnix: 1}
	two := legacyBucketKey{controller: "A", action: "B\x00C", kind: "unknown", outcome: "unknown", state: "valid", atUnix: 1}
	if one == two {
		t.Fatal("distinct controller/action pairs collided")
	}
}

func TestLegacyStatusClassificationKeepsDecimalTextUnknown(t *testing.T) {
	for _, item := range []struct {
		value any
		want  string
	}{{"201", "success"}, {int32(500), "failure"}, {"Succeeded", "success"}, {"201.5", "unknown"}} {
		data, err := bson.Marshal(bson.D{{Key: "value", Value: item.value}})
		if err != nil {
			t.Fatal(err)
		}
		got := legacyOutcome("hangfire", bson.Raw(data).Lookup("value"))
		if got != item.want {
			t.Errorf("legacyOutcome(%v) = %q, want %q", item.value, got, item.want)
		}
	}
}

func TestLegacyStatusDoesNotConvertDatesToNumericCodes(t *testing.T) {
	encoded, err := bson.Marshal(bson.D{{Key: "status", Value: time.UnixMilli(200)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := legacyOutcome("synchronous", bson.Raw(encoded).Lookup("status")); got != "unknown" {
		t.Fatalf("date-valued status outcome = %q, want unknown", got)
	}
}

func TestLegacyNumberPreservesBooleanAndBSONDateConversion(t *testing.T) {
	date := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	encoded, err := bson.Marshal(bson.D{{Key: "bool", Value: true}, {Key: "date", Value: date}})
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(encoded)
	if value := legacyNumber(raw.Lookup("bool")); value == nil || *value != 1 {
		t.Fatalf("boolean double conversion = %v, want 1", value)
	}
	wantMillis := float64(date.UnixMilli())
	if value := legacyNumber(raw.Lookup("date")); value == nil || *value != wantMillis {
		t.Fatalf("date double conversion = %v, want %v", value, wantMillis)
	}
}

func TestLegacyScanLimitCarriesBothGeneralAndSpecificSentinels(t *testing.T) {
	err := legacyScanLimit("record")
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrLegacyScanLimit) {
		t.Fatalf("legacy scan error must preserve both sentinels: %v", err)
	}
}

func TestLegacyRequestRowsRetainOnlyTopPageWindow(t *testing.T) {
	rows := make([]pageRow, 0, 2)
	for _, second := range []int64{1, 5, 3, 4, 2} {
		at := time.Unix(second, 0).UTC()
		insertPageRow(&rows, pageRow{at: at, object: bson.NewObjectID()}, 2)
	}
	if len(rows) != 2 || !rows[0].at.Equal(time.Unix(5, 0)) || !rows[1].at.Equal(time.Unix(4, 0)) {
		t.Fatalf("bounded page window = %#v", rows)
	}
}

func TestLegacyReferenceCompactsTimeSlotsAndRetainsScanAccounting(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "audit")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for _, tc := range []struct {
		name  string
		start time.Time
		want  int
	}{{"reference", q.BaselineStart, 1}, {"current", q.Start, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			var docs []any
			for _, offset := range []time.Duration{time.Second, 11 * time.Second} {
				at := tc.start.Add(offset)
				encoded, e := bson.Marshal(bson.D{{Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestPollingType", Value: "synchronous"}, {Key: "StatusCode", Value: 200}, {Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(12 * time.Millisecond)}, {Key: "ApiTime", Value: 12}})
				if e != nil {
					t.Fatal(e)
				}
				docs = append(docs, bson.Raw(encoded))
			}
			handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
				return &fakeCursor{docs: docs}, nil
			}
			var rows, bytes, records, rawBytes atomic.Int64
			got, e := readLegacyBuckets(context.Background(), p, q, tc.start, nil, &rows, &bytes, &records, &rawBytes)
			if e != nil {
				t.Fatal(e)
			}
			if len(got) != tc.want || rows.Load() != int64(tc.want) {
				t.Fatalf("summary rows=%d want=%d", len(got), tc.want)
			}
			if records.Load() != 2 || rawBytes.Load() == 0 {
				t.Fatal("raw scan accounting changed")
			}
			var count int64
			var sum float64
			for _, b := range got {
				count += b.Count
				sum += b.SumMs
				if tc.name == "reference" && !b.At.Equal(q.BaselineStart) {
					t.Fatal("reference timestamp not collapsed")
				}
			}
			if count != 2 || sum != 24 {
				t.Fatalf("lost timings count=%d sum=%f", count, sum)
			}
		})
	}
}
