package store

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBuildPipelineStartsWithHalfOpenIndexedTimingMatch(t *testing.T) {
	start := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(15 * time.Minute)
	q := audit.Query{BucketSeconds: 30, Metric: "total", Kind: "synchronous", Outcome: "success", Controller: "Payroll", Action: "Run"}
	pipeline := buildPipeline(q, start, end)
	if len(pipeline) < 5 {
		t.Fatalf("pipeline has only %d stages", len(pipeline))
	}
	first, ok := pipeline[0][0].Value.(bson.D)
	if !ok {
		t.Fatalf("first stage has type %T", pipeline[0][0].Value)
	}
	serialized, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(serialized)
	for _, want := range []string{"RequestInitiatedAt", "CreatedOn", "$gte", "$lt", "2026-10-22T00:00:00Z", "Payroll", "Run"} {
		if !strings.Contains(text, want) {
			t.Errorf("pipeline JSON missing %q: %s", want, text)
		}
	}
	_ = first
	last := pipeline[len(pipeline)-2]
	if len(last) != 1 || last[0].Key != "$limit" || last[0].Value != maxBucketRows {
		t.Fatalf("summary row cap stage = %v, want %d", last, maxBucketRows)
	}
}

func TestBuildPipelineNormalizesKindsGuardsDatesAndOmitsPayload(t *testing.T) {
	q := audit.Query{BucketSeconds: 10, Metric: "api", Kind: "all", Outcome: "all"}
	pipeline := buildPipeline(q, time.Now().Add(-time.Hour), time.Now())
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{"$trim", "RequestPollingType", "UserAgent", "$type", "RequestCompletedAt", "ApiTime", "$switch", "1.1"} {
		if !strings.Contains(text, want) {
			t.Errorf("pipeline is missing %q", want)
		}
	}
	for _, forbidden := range []string{"RequestPayload", "ResponsePayload", "$push", "$dateTrunc", "$percentile"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("pipeline contains forbidden %q", forbidden)
		}
	}
}

func TestAggregateCommandBoundsCursorAndServerExecution(t *testing.T) {
	command := aggregateCommand("Audit", buildPipeline(audit.Query{BucketSeconds: 10, Metric: "total", Kind: "all", Outcome: "all"}, time.Now().Add(-time.Minute), time.Now()))
	encoded, err := bson.MarshalExtJSON(command, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{`"batchSize":100`, `"allowDiskUse":false`, `"aggregate":"Audit"`} {
		if !strings.Contains(text, want) {
			t.Errorf("aggregate command missing %s", want)
		}
	}
}

func TestReferencePipelineRetainsFiltersAndHistogramWithoutTimelineGrouping(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Controller = "Payroll"
	q.Action = "Run"
	current := buildSummaryPipeline(q, q.Start, q.End, true)
	reference := buildSummaryPipeline(q, q.BaselineStart, q.BaselineEnd, false)
	if len(current) != len(reference) {
		t.Fatal("pipeline stages changed")
	}
	for i := range reference {
		if reference[i][0].Key != "$set" {
			continue
		}
		fields, ok := reference[i][0].Value.(bson.D)
		if !ok {
			continue
		}
		for _, field := range fields {
			if field.Key == "_auditBucketAt" {
				literal, ok := field.Value.(bson.D)
				if !ok || len(literal) != 1 || literal[0].Key != "$literal" || literal[0].Value != q.BaselineStart.UTC() {
					t.Fatalf("reference timeline not compacted: %v", field.Value)
				}
			}
		}
	}
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: reference}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Payroll", "Run", "$_id.bin", "$sum", "$min", "$max", "RequestInitiatedAt"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("reference pipeline lost %s", want)
		}
	}
}
