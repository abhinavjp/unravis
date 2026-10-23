package store

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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

func TestStringsToBSONHasNoNilEntries(t *testing.T) {
	got := stringsToBSON("OK", "CREATED")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %v", len(got), got)
	}
	if got[0] != "OK" || got[1] != "CREATED" {
		t.Fatalf("unexpected entries: %v", got)
	}
}

// collectInSets finds every {$in: [<expr>, <array>]} in v and returns the arrays.
func collectInSets(v any, out *[]bson.A) {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			if e.Key == "$in" {
				if args, ok := e.Value.(bson.A); ok && len(args) == 2 {
					if set, ok := args[1].(bson.A); ok {
						*out = append(*out, set)
					}
				}
			}
			collectInSets(e.Value, out)
		}
	case bson.A:
		for _, item := range x {
			collectInSets(item, out)
		}
	}
}

func TestSummaryPipelineMissingStatusIsUnknown(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	pipeline := buildSummaryPipeline(q, q.Start, q.End, true)
	var sets []bson.A
	for _, stage := range pipeline {
		collectInSets(stage, &sets)
	}
	var statusSets []bson.A
	for _, set := range sets {
		if len(set) > 0 && set[len(set)-1] == "PERMANENTREDIRECT" || len(set) > 0 && set[len(set)-1] == "NETWORKAUTHENTICATIONREQUIRED" {
			statusSets = append(statusSets, set)
		}
	}
	if len(statusSets) != 2 {
		t.Fatalf("found %d status $in arrays, want 2 (success, failure)", len(statusSets))
	}
	for _, set := range statusSets {
		rendered, err := bson.MarshalExtJSON(bson.D{{Key: "set", Value: set}}, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(rendered), "null") {
			t.Fatalf("status $in array contains null: %s", rendered)
		}
	}
	sets2 := statusNames()
	if len(statusSets[0]) != len(sets2.success) || len(statusSets[1]) != len(sets2.failure) {
		t.Fatalf("set sizes diverge from statusNames: success=%d failure=%d", len(statusSets[0]), len(statusSets[1]))
	}
	for _, set := range statusSets {
		for _, entry := range set {
			if s, ok := entry.(string); !ok || s == "" {
				t.Fatalf("status set has non-string or empty entry: %#v", entry)
			}
		}
	}
}

func groupStage(t *testing.T, pipeline mongo.Pipeline) bson.D {
	t.Helper()
	for _, stage := range pipeline {
		if stage[0].Key == "$group" {
			return stage[0].Value.(bson.D)
		}
	}
	t.Fatal("pipeline has no $group stage")
	return nil
}

func groupField(group bson.D, key string) any {
	for _, e := range group {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

func TestPipelineGroupsPhaseSumsWithPerPhaseCounts(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	phases := []struct{ sum, count, field, temp string }{
		{"sumAuditMs", "nAudit", "AuditCreateTime", "_auditAuditMs"},
		{"sumApiMs", "nApi", "ApiTime", "_auditApiMs"},
		{"sumPayloadMs", "nPayload", "PayloadRecordTime", "_auditPayloadMs"},
	}
	for _, metric := range []string{"total", "api"} {
		q.Metric = metric
		pipeline := buildPipeline(q, q.Start, q.End)
		group := groupStage(t, pipeline)
		for _, phase := range phases {
			for _, key := range []string{phase.sum, phase.count} {
				expr, ok := groupField(group, key).(bson.D)
				if !ok || len(expr) != 1 || expr[0].Key != "$sum" {
					t.Fatalf("%s/%s: $group field is %v, want a $sum", metric, key, groupField(group, key))
				}
			}
			// Both the value sum and the presence count are gated by $cond on a valid
			// document whose extracted phase is >= 0 and < MaxFloat64.
			for _, key := range []string{phase.sum, phase.count} {
				gate := `{"$cond":[{"$and":[{"$eq":["$_auditState","valid"]},{"$gte":["` + "$" + phase.temp + `",0]},{"$lt":["` + "$" + phase.temp + `",1.7976931348623157E+308]}]},`
				text, err := bson.MarshalExtJSON(bson.D{{Key: "e", Value: groupField(group, key)}}, false, false)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(text), gate) {
					t.Errorf("%s/%s lacks the presence gate %s: %s", metric, key, gate, text)
				}
				// The else branch adds nothing, and a count adds exactly 1 per present document.
				want := `"$` + phase.temp + `",0]}}}`
				if key == phase.count {
					want = `,1,0]}}}`
				}
				if !strings.HasSuffix(string(text), want) {
					t.Errorf("%s/%s has the wrong then/else branches: %s", metric, key, text)
				}
			}
			encoded, err := bson.MarshalExtJSON(bson.D{{Key: "e", Value: groupField(group, phase.sum)}}, false, false)
			if err != nil {
				t.Fatal(err)
			}
			// The sum adds the extracted phase value, never a literal count of documents.
			if !strings.Contains(string(encoded), phase.temp) {
				t.Errorf("%s/%s does not sum the extracted %s: %s", metric, phase.sum, phase.field, encoded)
			}
		}
		// Phase values use the same $convert-to-double extraction as ApiTime, so units stay milliseconds.
		whole, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
		if err != nil {
			t.Fatal(err)
		}
		text := string(whole)
		for _, phase := range phases {
			convert := `"input":"$` + phase.field + `","to":"double","onError":null,"onNull":null`
			if !strings.Contains(text, convert) {
				t.Errorf("%s: %s is not extracted with $convert to double: %s", metric, phase.field, text)
			}
		}
		for _, forbidden := range []string{"$multiply", "$divide", "$toDouble"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s: pipeline rescales units with %s", metric, forbidden)
			}
		}
	}
}

func TestPipelinePhaseFieldsAreProjected(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	pipeline := buildPipeline(q, q.Start, q.End)
	project := pipeline[len(pipeline)-1][0].Value.(bson.D)
	for _, key := range []string{"sumAuditMs", "sumApiMs", "sumPayloadMs", "nAudit", "nApi", "nPayload"} {
		if groupField(project, key) == nil {
			t.Errorf("$project drops %s", key)
		}
	}
}
