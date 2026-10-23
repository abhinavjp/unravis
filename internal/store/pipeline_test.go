package store

import (
	"reflect"
	"strconv"
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

// pipelineMissing marks a field that is absent from the document.
type pipelineMissing struct{}

func pipelineNum(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func pipelineTypeName(v any) string {
	switch v.(type) {
	case pipelineMissing:
		return "missing"
	case nil:
		return "null"
	case time.Time:
		return "date"
	case string:
		return "string"
	}
	return "double"
}

// evalExpr evaluates the subset of aggregation expressions that the _audit*
// $set stages use, against one document. It is a test double for the server.
func evalExpr(t *testing.T, expr any, doc map[string]any) any {
	t.Helper()
	switch x := expr.(type) {
	case string:
		if strings.HasPrefix(x, "$") {
			if v, ok := doc[x[1:]]; ok {
				return v
			}
			return pipelineMissing{}
		}
		return x
	case bson.A:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = evalExpr(t, item, doc)
		}
		return out
	case bson.D:
		if len(x) == 0 {
			t.Fatal("empty expression document")
		}
		op, arg := x[0].Key, x[0].Value
		args := func() []any {
			list, ok := arg.(bson.A)
			if !ok {
				t.Fatalf("%s takes an array, got %T", op, arg)
			}
			out := make([]any, len(list))
			for i, item := range list {
				out[i] = evalExpr(t, item, doc)
			}
			return out
		}
		field := func(name string) any {
			for _, e := range arg.(bson.D) {
				if e.Key == name {
					return e.Value
				}
			}
			return nil
		}
		switch op {
		case "$literal":
			return arg
		case "$type":
			return pipelineTypeName(evalExpr(t, arg, doc))
		case "$ifNull":
			a := args()
			if _, missing := a[0].(pipelineMissing); missing || a[0] == nil {
				return a[1]
			}
			return a[0]
		case "$trim":
			return strings.TrimSpace(evalExpr(t, field("input"), doc).(string))
		case "$toLower":
			return strings.ToLower(evalExpr(t, arg, doc).(string))
		case "$toUpper":
			return strings.ToUpper(evalExpr(t, arg, doc).(string))
		case "$convert":
			in := evalExpr(t, field("input"), doc)
			to := field("to").(string)
			if _, missing := in.(pipelineMissing); missing || in == nil {
				return field("onNull")
			}
			switch to {
			case "string":
				if s, ok := in.(string); ok {
					return s
				}
				return field("onError")
			case "int", "double":
				if n, ok := pipelineNum(in); ok {
					return n
				}
				if s, ok := in.(string); ok {
					if n, err := strconv.ParseFloat(s, 64); err == nil {
						return n
					}
				}
				return field("onError")
			}
			t.Fatalf("unsupported $convert target %q", to)
		case "$eq":
			a := args()
			an, aok := pipelineNum(a[0])
			bn, bok := pipelineNum(a[1])
			if aok && bok {
				return an == bn
			}
			return reflect.DeepEqual(a[0], a[1])
		case "$ne":
			a := args()
			return !reflect.DeepEqual(a[0], a[1])
		case "$gte", "$lt":
			a := args()
			an, aok := pipelineNum(a[0])
			bn, bok := pipelineNum(a[1])
			if !aok || !bok {
				return false
			}
			if op == "$gte" {
				return an >= bn
			}
			return an < bn
		case "$and":
			for _, v := range args() {
				if b, _ := v.(bool); !b {
					return false
				}
			}
			return true
		case "$or":
			for _, v := range args() {
				if b, _ := v.(bool); b {
					return true
				}
			}
			return false
		case "$in":
			a := args()
			for _, item := range a[1].([]any) {
				if reflect.DeepEqual(item, a[0]) {
					return true
				}
			}
			return false
		case "$subtract":
			a := args()
			l, lok := a[0].(time.Time)
			r, rok := a[1].(time.Time)
			if !lok || !rok {
				t.Fatalf("$subtract on %T/%T", a[0], a[1])
			}
			return float64(l.UnixMilli() - r.UnixMilli())
		case "$cond":
			a, _ := arg.(bson.A)
			if b, _ := evalExpr(t, a[0], doc).(bool); b {
				return evalExpr(t, a[1], doc)
			}
			return evalExpr(t, a[2], doc)
		case "$switch":
			for _, branch := range field("branches").(bson.A) {
				pair := branch.(bson.D)
				if b, _ := evalExpr(t, pair[0].Value, doc).(bool); b {
					return evalExpr(t, pair[1].Value, doc)
				}
			}
			return evalExpr(t, field("default"), doc)
		}
		t.Fatalf("unsupported operator %s", op)
	}
	return expr
}

// evalSetStages runs the pipeline's $set stages over doc and returns the
// document with its derived fields. The bucket slot and histogram bin are
// skipped because they do not influence state or outcome.
func evalSetStages(t *testing.T, pipeline mongo.Pipeline, doc map[string]any) map[string]any {
	t.Helper()
	derived := make(map[string]any, len(doc)+8)
	for k, v := range doc {
		derived[k] = v
	}
	for _, stage := range pipeline {
		if stage[0].Key == "$group" {
			break
		}
		if stage[0].Key != "$set" {
			continue
		}
		for _, e := range stage[0].Value.(bson.D) {
			if e.Key == "_auditBucketAt" || e.Key == "_auditBin" {
				continue
			}
			derived[e.Key] = evalExpr(t, e.Value, derived)
		}
	}
	return derived
}

type completionFixture struct {
	name                   string
	polling, agent, status string
	wantKind               string
	wantState              string
	wantOutcome            string
	wantPending            bool
	wantDurationNil        bool
}

func completionFixtures() []completionFixture {
	return []completionFixture{
		{"async accepted is pending", "Asynchronous ", "", "Accepted", "asynchronous", "incomplete", "unknown", true, true},
		{"async accepted is case-insensitive on the status", " asynchronous", "", "ACCEPTED", "asynchronous", "incomplete", "unknown", true, true},
		{"sync accepted is an inline 202", "Synchronous ", "", "Accepted", "synchronous", "valid", "success", false, false},
		{"async ok is complete", "Asynchronous ", "", "OK", "asynchronous", "valid", "success", false, false},
		{"async created is complete", "Asynchronous ", "", "Created", "asynchronous", "valid", "success", false, false},
		{"hangfire user agent keeps an async accepted row in the hangfire cohort", "Asynchronous ", "Hangfire", "Accepted", "hangfire", "valid", "success", false, false},
		{"hangfire polling type accepted is not pending", "Hangfire", "", "Accepted", "hangfire", "valid", "success", false, false},
	}
}

func completionDoc(fixture completionFixture) map[string]any {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	doc := map[string]any{
		"RequestPollingType": fixture.polling, "StatusCode": fixture.status,
		"RequestInitiatedAt": at, "RequestCompletedAt": at.Add(500 * time.Millisecond), "ApiTime": 480.0,
	}
	if fixture.agent != "" {
		doc["UserAgent"] = fixture.agent
	}
	return doc
}

func TestSummaryPipelineAsyncAcceptedIsIncomplete(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for _, metric := range []string{"total", "api"} {
		q.Metric = metric
		for _, fixture := range completionFixtures() {
			t.Run(metric+"/"+fixture.name, func(t *testing.T) {
				got := evalSetStages(t, buildSummaryPipeline(q, q.Start, q.End, true), completionDoc(fixture))
				if got["_auditKind"] != fixture.wantKind || got["_auditPending"] != fixture.wantPending {
					t.Fatalf("kind/pending = %v/%v, want %s/%v", got["_auditKind"], got["_auditPending"], fixture.wantKind, fixture.wantPending)
				}
				if got["_auditState"] != fixture.wantState || got["_auditOutcome"] != fixture.wantOutcome {
					t.Fatalf("state/outcome = %v/%v, want %s/%s", got["_auditState"], got["_auditOutcome"], fixture.wantState, fixture.wantOutcome)
				}
				if (got["_auditDuration"] == nil) != fixture.wantDurationNil {
					t.Fatalf("duration = %v, want nil=%v", got["_auditDuration"], fixture.wantDurationNil)
				}
			})
		}
	}
}

func TestRequestPipelineAsyncAcceptedHasUnknownOutcomeAndNoDuration(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for _, fixture := range completionFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			pipeline := requestPipeline(q, [5]string{"source", "", "", "all", "all"}, "source", nil, 10)
			got := evalSetStages(t, pipeline, completionDoc(fixture))
			if got["_auditKind"] != fixture.wantKind || got["_auditPending"] != fixture.wantPending {
				t.Fatalf("kind/pending = %v/%v, want %s/%v", got["_auditKind"], got["_auditPending"], fixture.wantKind, fixture.wantPending)
			}
			if got["_auditOutcome"] != fixture.wantOutcome || (got["_auditDuration"] == nil) != fixture.wantDurationNil {
				t.Fatalf("outcome/duration = %v/%v, want %s, nil=%v", got["_auditOutcome"], got["_auditDuration"], fixture.wantOutcome, fixture.wantDurationNil)
			}
		})
	}
}

// evalAccumulator evaluates the per-document expression inside a $group $sum
// accumulator and returns the amount that document contributes.
func evalAccumulator(t *testing.T, group bson.D, key string, doc map[string]any) float64 {
	t.Helper()
	accumulator, ok := groupField(group, key).(bson.D)
	if !ok || len(accumulator) != 1 || accumulator[0].Key != "$sum" {
		t.Fatalf("$group %s = %#v, want a single $sum accumulator", key, groupField(group, key))
	}
	value, ok := pipelineNum(evalExpr(t, accumulator[0].Value, doc))
	if !ok {
		t.Fatalf("$group %s did not evaluate to a number", key)
	}
	return value
}

func TestPipelineCountsPendingAsyncAndObservedPartial(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind = "all"
	for _, metric := range []string{"total", "api"} {
		q.Metric = metric
		pipeline := buildPipeline(q, q.Start, q.End)
		group := groupStage(t, pipeline)
		project := pipeline[len(pipeline)-1][0].Value.(bson.D)
		for _, key := range []string{"pendingAsync", "observedPartial"} {
			if groupField(group, key) == nil {
				t.Errorf("%s: $group lacks %s", metric, key)
			}
			if groupField(project, key) == nil {
				t.Errorf("%s: $project drops %s", metric, key)
			}
		}
		for _, fixture := range completionFixtures() {
			derived := evalSetStages(t, pipeline, completionDoc(fixture))
			wantPending := 0.0
			if fixture.wantPending {
				wantPending = 1
			}
			if got := evalAccumulator(t, group, "pendingAsync", derived); got != wantPending {
				t.Errorf("%s/%s: pendingAsync = %v, want %v", metric, fixture.name, got, wantPending)
			}
			// Every fixture carries RequestInitiatedAt, so none is partial.
			if got := evalAccumulator(t, group, "observedPartial", derived); got != 0 {
				t.Errorf("%s/%s: observedPartial = %v, want 0", metric, fixture.name, got)
			}
		}
		// A valid legacy row has no RequestInitiatedAt and falls back to CreatedOn:
		// partial under the total metric only. A pending or incomplete row never is.
		for _, fixture := range completionFixtures() {
			legacy := completionDoc(fixture)
			legacy["CreatedOn"] = legacy["RequestInitiatedAt"]
			delete(legacy, "RequestInitiatedAt")
			derived := evalSetStages(t, pipeline, legacy)
			want := 0.0
			if metric == "total" && fixture.wantState == "valid" {
				want = 1
			}
			if got := evalAccumulator(t, group, "observedPartial", derived); got != want {
				t.Errorf("%s/%s legacy: observedPartial = %v, want %v (state %v)", metric, fixture.name, got, want, derived["_auditState"])
			}
		}
	}
}
