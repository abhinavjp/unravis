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

func TestLegacyBucketsCarryPhaseSumsAndPerPhaseCounts(t *testing.T) {
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
	at := q.Start.Add(time.Second)
	doc := func(fields ...bson.E) any {
		base := bson.D{{Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestPollingType", Value: "synchronous"}, {Key: "StatusCode", Value: 200}, {Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(100 * time.Millisecond)}}
		encoded, e := bson.Marshal(append(base, fields...))
		if e != nil {
			t.Fatal(e)
		}
		return bson.Raw(encoded)
	}
	docs := []any{
		doc(bson.E{Key: "AuditCreateTime", Value: 20.0}, bson.E{Key: "ApiTime", Value: 80.0}, bson.E{Key: "PayloadRecordTime", Value: 5.0}),
		doc(bson.E{Key: "ApiTime", Value: "70"}),
		doc(bson.E{Key: "AuditCreateTime", Value: nil}, bson.E{Key: "ApiTime", Value: -3.0}),
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	var rows, bytes, records, rawBytes atomic.Int64
	got, err := readLegacyBuckets(context.Background(), p, q, q.Start, nil, &rows, &bytes, &records, &rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	var sumAudit, sumApi, sumPayload float64
	var nAudit, nApi, nPayload, count int64
	for _, b := range got {
		sumAudit, sumApi, sumPayload = sumAudit+b.SumAuditMs, sumApi+b.SumApiMs, sumPayload+b.SumPayloadMs
		nAudit, nApi, nPayload, count = nAudit+b.NAudit, nApi+b.NApi, nPayload+b.NPayload, count+b.Count
	}
	// The null audit time and the negative api time are not counted as present.
	if count != 3 || nAudit != 1 || nApi != 2 || nPayload != 1 {
		t.Fatalf("counts total=%d audit=%d api=%d payload=%d, want 3/1/2/1", count, nAudit, nApi, nPayload)
	}
	if sumAudit != 20 || sumApi != 150 || sumPayload != 5 {
		t.Fatalf("sums audit=%v api=%v payload=%v, want 20/150/5", sumAudit, sumApi, sumPayload)
	}
}

func legacyCompletionRaw(t *testing.T, fixture completionFixture) bson.Raw {
	t.Helper()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	encoded, err := bson.Marshal(bson.D{
		{Key: "_id", Value: bson.NewObjectID()}, {Key: "RequestPollingType", Value: fixture.polling}, {Key: "UserAgent", Value: fixture.agent}, {Key: "StatusCode", Value: fixture.status},
		{Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(500 * time.Millisecond)}, {Key: "ApiTime", Value: 480.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bson.Raw(encoded)
}

func TestLegacyAsyncAcceptedIsIncomplete(t *testing.T) {
	for _, metric := range []string{"total", "api"} {
		for _, fixture := range completionFixtures() {
			t.Run(metric+"/"+fixture.name, func(t *testing.T) {
				record, ok := normalizeLegacyRecord(legacyCompletionRaw(t, fixture), metric)
				if !ok {
					t.Fatal("record was not normalised")
				}
				if record.Kind != fixture.wantKind || record.PendingAsync != fixture.wantPending {
					t.Fatalf("kind/pending = %s/%v, want %s/%v", record.Kind, record.PendingAsync, fixture.wantKind, fixture.wantPending)
				}
				if record.State != fixture.wantState || record.Outcome != fixture.wantOutcome {
					t.Fatalf("state/outcome = %s/%s, want %s/%s", record.State, record.Outcome, fixture.wantState, fixture.wantOutcome)
				}
				if (record.Duration == nil) != fixture.wantDurationNil {
					t.Fatalf("duration = %v, want nil=%v", record.Duration, fixture.wantDurationNil)
				}
			})
		}
	}
}

func legacyBucketsFor(t *testing.T, q audit.Query, docs ...bson.D) []audit.Bucket {
	t.Helper()
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
	raws := make([]any, 0, len(docs))
	for _, d := range docs {
		encoded, e := bson.Marshal(d)
		if e != nil {
			t.Fatal(e)
		}
		raws = append(raws, bson.Raw(encoded))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var rows, bytes, records, rawBytes atomic.Int64
	got, err := readLegacyBuckets(context.Background(), p, q, q.Start, nil, &rows, &bytes, &records, &rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLegacyMissingInitiatedMarksObservedPartial(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind, q.Outcome = "all", "all"
	at := q.Start.Add(time.Second)
	base := func(fields ...bson.E) bson.D {
		return append(bson.D{{Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestPollingType", Value: "synchronous"}, {Key: "StatusCode", Value: "OK"}, {Key: "ApiTime", Value: 80.0}}, fields...)
	}
	onlyCreated := base(bson.E{Key: "CreatedOn", Value: at}, bson.E{Key: "RequestCompletedAt", Value: at.Add(80 * time.Millisecond)})
	full := base(bson.E{Key: "RequestInitiatedAt", Value: at}, bson.E{Key: "CreatedOn", Value: at}, bson.E{Key: "AuditCreateTime", Value: 20.0}, bson.E{Key: "RequestCompletedAt", Value: at.Add(100 * time.Millisecond)})
	raw, _ := bson.Marshal(onlyCreated)
	record, _ := normalizeLegacyRecord(bson.Raw(raw), "total")
	if !record.AuditCreateMissing || record.Duration == nil || *record.Duration != 80 || record.State != "valid" {
		t.Fatalf("CreatedOn-only record = %#v, want AuditCreateMissing with duration equal to ApiTime", record)
	}
	rawFull, _ := bson.Marshal(full)
	if complete, _ := normalizeLegacyRecord(bson.Raw(rawFull), "total"); complete.AuditCreateMissing {
		t.Fatal("a record with RequestInitiatedAt is not partial")
	}

	var partial, count int64
	var sum float64
	for _, b := range legacyBucketsFor(t, q, onlyCreated, full) {
		partial, count, sum = partial+b.ObservedPartial, count+b.Count, sum+b.SumMs
	}
	if partial != 1 || count != 2 || sum != 180 {
		t.Fatalf("observedPartial/count/sum = %d/%d/%v, want 1/2/180", partial, count, sum)
	}

	// Under the api metric nothing is partial: Observed time is not being measured.
	q.Metric = "api"
	for _, b := range legacyBucketsFor(t, q, onlyCreated) {
		if b.ObservedPartial != 0 {
			t.Fatalf("api metric reported observedPartial %d", b.ObservedPartial)
		}
	}
}

func TestLegacyBucketsCountPendingAsyncAsIncompleteWithoutTiming(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind, q.Outcome = "all", "all"
	at := q.Start.Add(time.Second)
	doc := func(polling, agent, status string) bson.D {
		return bson.D{{Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestPollingType", Value: polling}, {Key: "UserAgent", Value: agent}, {Key: "StatusCode", Value: status}, {Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(30 * time.Millisecond)}, {Key: "ApiTime", Value: 30.0}}
	}
	var pending, incompleteCount, validCount, hangfireCount int64
	var validSum float64
	for _, b := range legacyBucketsFor(t, q, doc("Asynchronous ", "", "Accepted"), doc("Asynchronous ", "", "Accepted"), doc("Synchronous ", "", "Accepted"), doc("Asynchronous ", "Hangfire", "Accepted")) {
		pending += b.PendingAsync
		if b.Kind == "hangfire" {
			hangfireCount += b.Count
			if b.State != "valid" || b.Outcome != "success" || b.PendingAsync != 0 {
				t.Fatalf("hangfire bucket %#v must be a valid success with no pending rows", b)
			}
		}
		switch b.State {
		case "incomplete":
			incompleteCount += b.Count
			if b.Outcome != "unknown" || b.SumMs != 0 {
				t.Fatalf("pending bucket %#v must have outcome unknown and no timing", b)
			}
		case "valid":
			validCount, validSum = validCount+b.Count, validSum+b.SumMs
			if b.Outcome != "success" || b.PendingAsync != 0 {
				t.Fatalf("valid bucket %#v must be a success with no pending rows", b)
			}
		}
	}
	if pending != 2 || incompleteCount != 2 || validCount != 2 || validSum != 60 || hangfireCount != 1 {
		t.Fatalf("pending/incomplete/valid/sum/hangfire = %d/%d/%d/%v/%d, want 2/2/2/60/1", pending, incompleteCount, validCount, validSum, hangfireCount)
	}
}

func legacyStatusDoc(polling, status any, at time.Time) bson.D {
	return bson.D{{Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestPollingType", Value: polling}, {Key: "StatusCode", Value: status}, {Key: "RequestInitiatedAt", Value: at}, {Key: "RequestCompletedAt", Value: at.Add(30 * time.Millisecond)}, {Key: "ApiTime", Value: 30.0}}
}

func legacyStatusTotals(buckets []audit.Bucket) (count, c4xx, c5xx, hangfireFailed int64) {
	for _, b := range buckets {
		count += b.Count
		c4xx += b.Count4xx
		c5xx += b.Count5xx
		hangfireFailed += b.CountHangfireFailed
	}
	return
}

func TestLegacyCounts4xx5xxSeparately(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind, q.Outcome = "all", "all"
	at := q.Start.Add(time.Second)
	for _, fixture := range statusClassFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			doc := legacyStatusDoc(fixture.polling, fixture.status, at)
			buckets := legacyBucketsFor(t, q, doc, doc)
			count, c4xx, c5xx, hf := legacyStatusTotals(buckets)
			if count != 2 || c4xx != int64(2*fixture.c4xx) || c5xx != int64(2*fixture.c5xx) || hf != int64(2*fixture.hf) {
				t.Fatalf("count/4xx/5xx/hangfire = %d/%d/%d/%d, want 2/%v/%v/%v", count, c4xx, c5xx, hf, 2*fixture.c4xx, 2*fixture.c5xx, 2*fixture.hf)
			}
			for _, b := range buckets {
				if want := audit.ClassOutcome(fixture.wantClass); b.Outcome != want {
					t.Fatalf("bucket outcome = %s, want %s", b.Outcome, want)
				}
			}
			// The Go classifier agrees with the server pipeline on every fixture.
			raw, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			record, ok := normalizeLegacyRecord(bson.Raw(raw), "total")
			if !ok || record.StatusClass != fixture.wantClass {
				t.Fatalf("legacy class = %q, want %q", record.StatusClass, fixture.wantClass)
			}
		})
	}
}

func TestLegacyOutcomeFiltersSelectStatusClasses(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind = "all"
	at := q.Start.Add(time.Second)
	docs := []bson.D{
		legacyStatusDoc("Synchronous", "NotFound", at),
		legacyStatusDoc("Synchronous", int32(502), at),
		legacyStatusDoc("Hangfire", "Failed", at),
		legacyStatusDoc("Synchronous", "OK", at),
		legacyStatusDoc("Synchronous", "Weird", at),
	}
	cases := []struct {
		outcome                  string
		count, c4xx, c5xx, hfail int64
	}{
		// Unknown outcomes stay in every filtered view so the quality counts stay honest.
		{"client_error", 2, 1, 0, 0},
		{"server_error", 3, 0, 1, 1},
		{"failure", 4, 1, 1, 1},
		{"success", 2, 0, 0, 0},
		{"all", 5, 1, 1, 1},
	}
	for _, c := range cases {
		q.Outcome = c.outcome
		count, c4xx, c5xx, hf := legacyStatusTotals(legacyBucketsFor(t, q, docs...))
		if count != c.count || c4xx != c.c4xx || c5xx != c.c5xx || hf != c.hfail {
			t.Errorf("%s: count/4xx/5xx/hangfire = %d/%d/%d/%d, want %d/%d/%d/%d", c.outcome, count, c4xx, c5xx, hf, c.count, c.c4xx, c.c5xx, c.hfail)
		}
	}
}

func legacyIncompleteDoc(polling, status any, at time.Time) bson.D {
	var doc bson.D
	for _, e := range legacyStatusDoc(polling, status, at) {
		if e.Key != "RequestCompletedAt" && e.Key != "ApiTime" {
			doc = append(doc, e)
		}
	}
	return doc
}

func TestLegacyClassFilterDropsKnownOtherClassWhateverTheState(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind = "all"
	at := q.Start.Add(time.Second)
	docs := []bson.D{
		legacyStatusDoc("Synchronous", "NotFound", at),
		legacyStatusDoc("Synchronous", "InternalServerError", at),
		legacyIncompleteDoc("Synchronous", "NotFound", at),
		legacyIncompleteDoc("Synchronous", "InternalServerError", at),
		legacyIncompleteDoc("Synchronous", "Weird", at),
		legacyIncompleteDoc("Asynchronous", "Accepted", at),
	}
	cases := []struct {
		outcome             string
		count, c4xx, c5xx   int64
		failureBucketsTotal int64
	}{
		// An incomplete 500 is a known server error, so a client_error view drops it,
		// and the reverse. Unknown and pending rows stay for the quality counts.
		{"client_error", 4, 2, 0, 2},
		{"server_error", 4, 0, 2, 2},
		{"failure", 6, 2, 2, 4},
		{"all", 6, 2, 2, 4},
	}
	for _, c := range cases {
		q.Outcome = c.outcome
		buckets := legacyBucketsFor(t, q, docs...)
		count, c4xx, c5xx, _ := legacyStatusTotals(buckets)
		if count != c.count || c4xx != c.c4xx || c5xx != c.c5xx {
			t.Errorf("%s: count/4xx/5xx = %d/%d/%d, want %d/%d/%d", c.outcome, count, c4xx, c5xx, c.count, c.c4xx, c.c5xx)
		}
		var failures int64
		for _, b := range buckets {
			if b.Outcome == "failure" {
				failures += b.Count
			}
		}
		if failures != c.failureBucketsTotal {
			t.Errorf("%s: failure-outcome rows = %d, want %d (the rows a requestVolume or drill-down would count)", c.outcome, failures, c.failureBucketsTotal)
		}
	}
}
