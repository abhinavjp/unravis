package store

import (
	"math"
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
		if len(set) > 0 && set[len(set)-1] == "IMUSED" || len(set) > 0 && set[len(set)-1] == "PERMANENTREDIRECT" || len(set) > 0 && set[len(set)-1] == "UNAVAILABLEFORLEGALREASONS" || len(set) > 0 && set[len(set)-1] == "NETWORKAUTHENTICATIONREQUIRED" {
			statusSets = append(statusSets, set)
		}
	}
	if len(statusSets) != 4 {
		t.Fatalf("found %d status $in arrays, want 4 (2xx, 3xx, 4xx, 5xx)", len(statusSets))
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
	want := statusClassSets()
	if len(statusSets[0]) != len(want.success) || len(statusSets[1]) != len(want.redirect) || len(statusSets[2]) != len(want.clientError) || len(statusSets[3]) != len(want.serverError) {
		t.Fatalf("set sizes diverge from statusClassSets: %d/%d/%d/%d", len(statusSets[0]), len(statusSets[1]), len(statusSets[2]), len(statusSets[3]))
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
			case "int":
				// A double truncates toward zero and a string must be an integer
				// (a leading sign and leading zeros are accepted, whitespace and
				// decimals are not). Anything outside the int32 range is an error.
				var n float64
				if v, ok := pipelineNum(in); ok {
					n = math.Trunc(v)
				} else if s, ok := in.(string); ok {
					parsed, err := strconv.ParseInt(s, 10, 64)
					if err != nil {
						return field("onError")
					}
					n = float64(parsed)
				} else {
					return field("onError")
				}
				if n < math.MinInt32 || n > math.MaxInt32 {
					return field("onError")
				}
				return n
			case "double":
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
			list, _ := a[1].([]any)
			if literal, ok := a[1].(bson.A); ok {
				list = literal
			}
			for _, item := range list {
				in, inOK := pipelineNum(item)
				want, wantOK := pipelineNum(a[0])
				if inOK && wantOK {
					if in == want {
						return true
					}
				} else if reflect.DeepEqual(item, a[0]) {
					return true
				}
			}
			return false
		case "$substrCP":
			a := args()
			runes := []rune(a[0].(string))
			start, _ := pipelineNum(a[1])
			length, _ := pipelineNum(a[2])
			if int(start) >= len(runes) {
				return ""
			}
			end := int(start) + int(length)
			if end > len(runes) {
				end = len(runes)
			}
			return string(runes[int(start):end])
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

// evalMatch reports whether doc satisfies a $match document of the shapes the
// summary and drill-down pipelines emit: field equality, $in, $ne and $type
// conditions combined with $or and $and. Anything else fails the test.
func evalMatch(t *testing.T, match bson.D, doc map[string]any) bool {
	t.Helper()
	for _, e := range match {
		switch e.Key {
		case "$or", "$and":
			matched := e.Key == "$and"
			for _, branch := range e.Value.(bson.A) {
				ok := evalMatch(t, branch.(bson.D), doc)
				if e.Key == "$or" && ok {
					matched = true
				}
				if e.Key == "$and" && !ok {
					matched = false
				}
			}
			if !matched {
				return false
			}
		case "$expr":
			if ok, _ := evalExpr(t, e.Value, doc).(bool); !ok {
				return false
			}
		default:
			value, present := doc[e.Key]
			if !present {
				value = pipelineMissing{}
			}
			condition, isOps := e.Value.(bson.D)
			if !isOps {
				if !reflect.DeepEqual(value, e.Value) {
					return false
				}
				continue
			}
			for _, op := range condition {
				switch op.Key {
				case "$ne":
					if reflect.DeepEqual(value, op.Value) {
						return false
					}
				case "$in":
					found := false
					for _, item := range op.Value.(bson.A) {
						found = found || reflect.DeepEqual(value, item)
					}
					if !found {
						return false
					}
				case "$type":
					if pipelineTypeName(value) != op.Value {
						return false
					}
				default:
					t.Fatalf("unsupported $match operator %s on %s", op.Key, e.Key)
				}
			}
		}
	}
	return true
}

// evalRowSurvives runs doc through the pipeline's $set stages and then every
// $match stage after the first (the raw-document prefilter), up to any $group.
// It returns the derived document and whether the row stays in.
func evalRowSurvives(t *testing.T, pipeline mongo.Pipeline, doc map[string]any) (map[string]any, bool) {
	t.Helper()
	derived := evalSetStages(t, pipeline, doc)
	for i, stage := range pipeline {
		if stage[0].Key == "$group" {
			break
		}
		if i == 0 || stage[0].Key != "$match" {
			continue
		}
		if !evalMatch(t, stage[0].Value.(bson.D), derived) {
			return derived, false
		}
	}
	return derived, true
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

type statusClassFixture struct {
	name            string
	polling, status any
	wantClass       string
	c4xx, c5xx, hf  float64
}

func statusClassFixtures() []statusClassFixture {
	return []statusClassFixture{
		{"sync OK", "Synchronous", "OK", "success", 0, 0, 0},
		{"sync 200 text", "Synchronous", "200", "success", 0, 0, 0},
		{"sync redirect alias", "Synchronous", "Redirect", "redirect", 0, 0, 0},
		{"sync 304 number", "Synchronous", int32(304), "redirect", 0, 0, 0},
		{"sync NotFound", "Synchronous", "NotFound", "client_error", 1, 0, 0},
		{"sync 404 text", "Synchronous", "404", "client_error", 1, 0, 0},
		{"sync 429 number", "Synchronous", int32(429), "client_error", 1, 0, 0},
		{"sync RequestEntityTooLarge alias", "Synchronous", "RequestEntityTooLarge", "client_error", 1, 0, 0},
		{"sync UnprocessableContent alias", "Synchronous", "UnprocessableContent", "client_error", 1, 0, 0},
		{"sync InternalServerError", "Synchronous", "InternalServerError", "server_error", 0, 1, 0},
		{"sync 502 text", "Synchronous", "502", "server_error", 0, 1, 0},
		{"sync 503 number", "Synchronous", int32(503), "server_error", 0, 1, 0},
		{"hangfire failed", "Hangfire", "Failed", "hangfire_failed", 0, 0, 1},
		{"hangfire succeeded", "Hangfire", "Succeeded", "success", 0, 0, 0},
		{"sync Failed is not a hangfire status", "Synchronous", "Failed", "unknown", 0, 0, 0},
		{"async Accepted is pending", "Asynchronous", "Accepted", "pending", 0, 0, 0},
		{"missing status", "Synchronous", "", "unknown", 0, 0, 0},
		{"unrecognised status", "Synchronous", "Weird", "unknown", 0, 0, 0},
		{"1xx status", "Synchronous", "Continue", "unknown", 0, 0, 0},
		{"out of range number", "Synchronous", int32(700), "unknown", 0, 0, 0},
	}
}

func statusClassDoc(fixture statusClassFixture) map[string]any {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return map[string]any{
		"RequestPollingType": fixture.polling, "StatusCode": fixture.status,
		"RequestInitiatedAt": at, "RequestCompletedAt": at.Add(500 * time.Millisecond), "ApiTime": 480.0,
	}
}

func TestSummaryPipelineCounts4xx5xxSeparately(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind = "all"
	pipeline := buildPipeline(q, q.Start, q.End)
	group := groupStage(t, pipeline)
	project := pipeline[len(pipeline)-1][0].Value.(bson.D)
	// The counters are accumulators, never group keys: the cohort key is unchanged.
	var keys []string
	for _, e := range groupField(group, "_id").(bson.D) {
		keys = append(keys, e.Key)
	}
	if want := []string{"controller", "action", "kind", "outcome", "state", "at", "bin"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("$group _id keys = %v, want %v", keys, want)
	}
	for _, key := range []string{"c4xx", "c5xx", "cHangfireFailed"} {
		if accumulator, ok := groupField(group, key).(bson.D); !ok || len(accumulator) != 1 || accumulator[0].Key != "$sum" {
			t.Errorf("$group %s = %#v, want one $sum accumulator", key, groupField(group, key))
		}
		if groupField(project, key) == nil {
			t.Errorf("$project drops %s", key)
		}
	}
	for _, fixture := range statusClassFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			derived := evalSetStages(t, pipeline, statusClassDoc(fixture))
			if derived["_statusClass"] != fixture.wantClass {
				t.Fatalf("_statusClass = %v, want %s", derived["_statusClass"], fixture.wantClass)
			}
			if want := audit.ClassOutcome(fixture.wantClass); derived["_auditOutcome"] != want {
				t.Fatalf("_auditOutcome = %v, want %s", derived["_auditOutcome"], want)
			}
			for key, want := range map[string]float64{"c4xx": fixture.c4xx, "c5xx": fixture.c5xx, "cHangfireFailed": fixture.hf} {
				if got := evalAccumulator(t, group, key, derived); got != want {
					t.Errorf("%s = %v, want %v", key, got, want)
				}
			}
		})
	}
	// 4xx and 5xx never share a counter: each row feeds at most one of the three.
	for _, fixture := range statusClassFixtures() {
		if fixture.c4xx+fixture.c5xx+fixture.hf > 1 {
			t.Errorf("%s feeds more than one error counter", fixture.name)
		}
	}
}

func TestSummaryPipelineStatusClassNamesAreDisjointByRange(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	var sets []bson.A
	for _, stage := range buildPipeline(q, q.Start, q.End) {
		collectInSets(stage, &sets)
	}
	bounds := map[string][2]int{"IMUSED": {200, 299}, "PERMANENTREDIRECT": {300, 399}, "UNAVAILABLEFORLEGALREASONS": {400, 499}, "NETWORKAUTHENTICATIONREQUIRED": {500, 599}}
	seen := 0
	for _, set := range sets {
		last, _ := set[len(set)-1].(string)
		bound, ok := bounds[last]
		if !ok {
			continue
		}
		seen++
		for _, entry := range set {
			code, ok := audit.StatusCodeNumber(entry.(string))
			if !ok || code < bound[0] || code > bound[1] {
				t.Errorf("set ending %s holds %v = %d, outside %d-%d", last, entry, code, bound[0], bound[1])
			}
		}
	}
	if seen != 4 {
		t.Fatalf("found %d class name sets, want 4", seen)
	}
}

func TestSummaryPipelineOutcomeFilterSelectsStatusClasses(t *testing.T) {
	filters := map[string][]string{
		"client_error": {"client_error"},
		"server_error": {"server_error", "hangfire_failed"},
	}
	for outcome, classes := range filters {
		q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
		q.Outcome = outcome
		var match bson.D
		for _, stage := range buildPipeline(q, q.Start, q.End) {
			if stage[0].Key != "$match" {
				continue
			}
			if text, _ := bson.MarshalExtJSON(stage, false, false); strings.Contains(string(text), "_statusClass") {
				match = stage[0].Value.(bson.D)
			}
		}
		if match == nil {
			t.Fatalf("%s: no outcome $match on _statusClass", outcome)
		}
		encoded, err := bson.MarshalExtJSON(match, false, false)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, class := range []string{"client_error", "server_error", "hangfire_failed"} {
			want := false
			for _, c := range classes {
				want = want || c == class
			}
			if strings.Contains(text, `"`+class+`"`) != want {
				t.Errorf("%s: $match mentions %s = %v, want %v: %s", outcome, class, !want, want, text)
			}
		}
		// Rows with an unknown outcome stay in, so the quality counts remain
		// honest. A class filter selects by class whatever the row state, so it
		// has no state clause that would let a known other-class row through.
		if !strings.Contains(text, `"_auditOutcome":"unknown"`) {
			t.Errorf("%s: $match dropped the unknown outcome: %s", outcome, text)
		}
		if strings.Contains(text, `"_auditState"`) {
			t.Errorf("%s: $match keeps non-valid rows of other classes: %s", outcome, text)
		}
	}
	// success and failure keep matching on the coarse outcome.
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Outcome = "failure"
	whole, _ := bson.MarshalExtJSON(bson.D{{Key: "p", Value: buildPipeline(q, q.Start, q.End)}}, false, false)
	if !strings.Contains(string(whole), `{"_auditOutcome":"failure"}`) {
		t.Errorf("failure filter lost its coarse outcome match")
	}
}

// incompleteDoc is a status fixture row with no completion time, so its state is
// incomplete (not valid) while its status class is still known.
func incompleteDoc(fixture statusClassFixture) map[string]any {
	doc := statusClassDoc(fixture)
	delete(doc, "RequestCompletedAt")
	delete(doc, "ApiTime")
	return doc
}

func TestSummaryPipelineClassFilterDropsKnownOtherClassWhateverTheState(t *testing.T) {
	variants := []struct {
		name string
		doc  func(statusClassFixture) map[string]any
		want string
	}{
		{"complete", statusClassDoc, "valid"},
		{"incomplete", incompleteDoc, "incomplete"},
	}
	for _, outcome := range []string{"client_error", "server_error"} {
		q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
		q.Kind, q.Outcome = "all", outcome
		pipeline := buildPipeline(q, q.Start, q.End)
		for _, variant := range variants {
			for _, fixture := range statusClassFixtures() {
				t.Run(outcome+"/"+variant.name+"/"+fixture.name, func(t *testing.T) {
					derived, kept := evalRowSurvives(t, pipeline, variant.doc(fixture))
					if derived["_statusClass"] != fixture.wantClass {
						t.Fatalf("_statusClass = %v, want %s", derived["_statusClass"], fixture.wantClass)
					}
					if fixture.wantClass != "pending" && derived["_auditState"] != variant.want {
						t.Fatalf("_auditState = %v, want %s", derived["_auditState"], variant.want)
					}
					coarse := audit.ClassOutcome(fixture.wantClass)
					want := coarse == "unknown" || audit.OutcomeFilterMatches(outcome, coarse, fixture.wantClass)
					if kept != want {
						t.Fatalf("kept = %v, want %v (class %s, state %v)", kept, want, fixture.wantClass, derived["_auditState"])
					}
				})
			}
		}
	}
	// The two cases the review named, spelled out.
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind = "all"
	incomplete500 := incompleteDoc(statusClassFixture{polling: "Synchronous", status: "InternalServerError"})
	incomplete404 := incompleteDoc(statusClassFixture{polling: "Synchronous", status: "NotFound"})
	cases := []struct {
		outcome string
		doc     map[string]any
		want    bool
	}{
		{"client_error", incomplete500, false},
		{"client_error", incomplete404, true},
		{"server_error", incomplete404, false},
		{"server_error", incomplete500, true},
	}
	for _, c := range cases {
		q.Outcome = c.outcome
		if _, kept := evalRowSurvives(t, buildPipeline(q, q.Start, q.End), c.doc); kept != c.want {
			t.Errorf("incomplete %v under %s: kept = %v, want %v", c.doc["StatusCode"], c.outcome, kept, c.want)
		}
	}
	// success and failure still keep non-valid rows so the quality counts stay honest.
	q.Outcome = "success"
	if _, kept := evalRowSurvives(t, buildPipeline(q, q.Start, q.End), incomplete500); !kept {
		t.Error("an incomplete 500 must stay in a success query so the quality counts see it")
	}
}

func TestSummaryCohortCountsReconcileWithDrillDownUnderClassFilters(t *testing.T) {
	var docs []map[string]any
	for _, fixture := range statusClassFixtures() {
		docs = append(docs, statusClassDoc(fixture), incompleteDoc(fixture))
	}
	for _, outcome := range []string{"client_error", "server_error"} {
		q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
		q.Kind, q.Outcome = "all", outcome
		summary := buildPipeline(q, q.Start, q.End)
		group := groupStage(t, summary)
		drill := requestPipeline(q, [5]string{"source", "", "", "all", "failure"}, "source", nil, 100)
		var cohortRows, classRows, drillRows int
		for _, doc := range docs {
			derived, kept := evalRowSurvives(t, summary, doc)
			if kept && derived["_auditOutcome"] == "failure" {
				cohortRows++
				for _, key := range []string{"c4xx", "c5xx", "cHangfireFailed"} {
					classRows += int(evalAccumulator(t, group, key, derived))
				}
			}
			if _, inDrill := evalRowSurvives(t, drill, doc); inDrill {
				drillRows++
			}
		}
		// requestVolume and the stats count the failure cohort; the drill-down lists
		// it; the class counters split it. All three must agree.
		if cohortRows == 0 || cohortRows != drillRows || cohortRows != classRows {
			t.Errorf("%s: failure cohort rows = %d, drill-down rows = %d, 4xx+5xx+hangfire = %d; want all equal and non-zero", outcome, cohortRows, drillRows, classRows)
		}
	}
}

func TestRequestPipelineFiltersByStatusClass(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Outcome = "client_error"
	cases := []struct {
		name   string
		cohort [5]string
		want   []string
		reject []string
	}{
		{"query filter only", [5]string{"source", "", "", "all", "client_error"}, []string{`"_statusClass":"client_error"`}, []string{`"_auditOutcome":"client_error"`}},
		{"api key narrows to failure", [5]string{"source", "", "", "all", "failure"}, []string{`"_auditOutcome":"failure"`, `"_statusClass":"client_error"`}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pipeline := requestPipeline(q, c.cohort, "source", nil, 10)
			encoded, err := bson.MarshalExtJSON(bson.D{{Key: "p", Value: pipeline}}, false, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range c.want {
				if !strings.Contains(string(encoded), want) {
					t.Errorf("pipeline lacks %s", want)
				}
			}
			for _, reject := range c.reject {
				if strings.Contains(string(encoded), reject) {
					t.Errorf("pipeline contains %s", reject)
				}
			}
			for _, fixture := range statusClassFixtures() {
				derived := evalSetStages(t, pipeline, statusClassDoc(fixture))
				if derived["_statusClass"] != fixture.wantClass || derived["_auditOutcome"] != audit.ClassOutcome(fixture.wantClass) {
					t.Errorf("%s: class/outcome = %v/%v, want %s/%s", fixture.name, derived["_statusClass"], derived["_auditOutcome"], fixture.wantClass, audit.ClassOutcome(fixture.wantClass))
				}
			}
		})
	}
	if err := validateCohortQuery(audit.Query{SourceIDs: []string{"source"}, Kind: "all", Outcome: "client_error"}, [5]string{"source", "", "", "all", "failure"}); err != nil {
		t.Errorf("a failure cohort must satisfy a client_error query: %v", err)
	}
	if err := validateCohortQuery(audit.Query{SourceIDs: []string{"source"}, Kind: "all", Outcome: "server_error"}, [5]string{"source", "", "", "all", "success"}); err == nil {
		t.Error("a success cohort must not satisfy a server_error query")
	}
}

// dotNetStatusTable repeats the System.Net.HttpStatusCode enum, aliases and the
// names newer .NET versions added, independently of audit.statusCodes, so the
// agreement test below catches a name that one side forgot.
var dotNetStatusTable = []struct {
	name string
	code int
}{
	{"Continue", 100}, {"SwitchingProtocols", 101}, {"Processing", 102}, {"EarlyHints", 103},
	{"OK", 200}, {"Created", 201}, {"Accepted", 202}, {"NonAuthoritativeInformation", 203}, {"NoContent", 204},
	{"ResetContent", 205}, {"PartialContent", 206}, {"MultiStatus", 207}, {"AlreadyReported", 208}, {"IMUsed", 226},
	{"MultipleChoices", 300}, {"Ambiguous", 300}, {"MovedPermanently", 301}, {"Moved", 301},
	{"Found", 302}, {"Redirect", 302}, {"SeeOther", 303}, {"RedirectMethod", 303},
	{"NotModified", 304}, {"UseProxy", 305}, {"Unused", 306},
	{"TemporaryRedirect", 307}, {"RedirectKeepVerb", 307}, {"PermanentRedirect", 308},
	{"BadRequest", 400}, {"Unauthorized", 401}, {"PaymentRequired", 402}, {"Forbidden", 403}, {"NotFound", 404},
	{"MethodNotAllowed", 405}, {"NotAcceptable", 406}, {"ProxyAuthenticationRequired", 407}, {"RequestTimeout", 408},
	{"Conflict", 409}, {"Gone", 410}, {"LengthRequired", 411}, {"PreconditionFailed", 412},
	{"RequestEntityTooLarge", 413}, {"PayloadTooLarge", 413}, {"ContentTooLarge", 413},
	{"RequestUriTooLong", 414}, {"UriTooLong", 414}, {"UnsupportedMediaType", 415},
	{"RequestedRangeNotSatisfiable", 416}, {"RangeNotSatisfiable", 416},
	{"ExpectationFailed", 417}, {"ImATeapot", 418}, {"MisdirectedRequest", 421},
	{"UnprocessableEntity", 422}, {"UnprocessableContent", 422},
	{"Locked", 423}, {"FailedDependency", 424}, {"TooEarly", 425}, {"UpgradeRequired", 426},
	{"PreconditionRequired", 428}, {"TooManyRequests", 429}, {"RequestHeaderFieldsTooLarge", 431}, {"UnavailableForLegalReasons", 451},
	{"InternalServerError", 500}, {"NotImplemented", 501}, {"BadGateway", 502}, {"ServiceUnavailable", 503},
	{"GatewayTimeout", 504}, {"HttpVersionNotSupported", 505}, {"VariantAlsoNegotiates", 506},
	{"InsufficientStorage", 507}, {"LoopDetected", 508}, {"NotExtended", 510}, {"NetworkAuthenticationRequired", 511},
}

func classForCode(code int) string {
	return map[int]string{2: "success", 3: "redirect", 4: "client_error", 5: "server_error"}[code/100]
}

// TestStatusClassifiersAgreeOnEveryStatusValue runs the Go classifier, the
// legacy (Mongo 3.4) reader and the Mongo pipeline expression over the same
// StatusCode values and asserts they all return the expected class.
func TestStatusClassifiersAgreeOnEveryStatusValue(t *testing.T) {
	type value struct {
		label  string
		status any
		want   string
	}
	var values []value
	for _, entry := range dotNetStatusTable {
		want := classForCode(entry.code)
		if want == "" {
			want = "unknown"
		}
		for _, text := range []string{entry.name, strings.ToUpper(entry.name), strings.ToLower(entry.name), strconv.Itoa(entry.code)} {
			values = append(values, value{"string " + text, text, want})
		}
		values = append(values,
			value{"int32 " + entry.name, int32(entry.code), want},
			value{"int64 " + entry.name, int64(entry.code), want},
			value{"double " + entry.name, float64(entry.code), want},
		)
	}
	values = append(values,
		value{"padded name", " NotFound ", "unknown"},
		value{"padded number", " 404 ", "unknown"},
		value{"zero text", "0", "unknown"},
		value{"999 text", "999", "unknown"},
		value{"minus one text", "-1", "unknown"},
		value{"decimal text", "404.0", "unknown"},
		value{"leading zeros", "0404", "client_error"},
		value{"leading plus", "+404", "client_error"},
		value{"leading plus 5xx", "+0503", "server_error"},
		value{"zero int32", int32(0), "unknown"},
		value{"999 int32", int32(999), "unknown"},
		value{"minus one int64", int64(-1), "unknown"},
		value{"huge int64", int64(5000000000), "unknown"},
		value{"999 double", float64(999), "unknown"},
		value{"fractional double", float64(404.9), "client_error"},
		value{"fractional 2xx double", float64(200.5), "success"},
		value{"empty", "", "unknown"},
	)
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind, q.Outcome = "all", "all"
	pipeline := buildPipeline(q, q.Start, q.End)
	for _, v := range values {
		t.Run(v.label, func(t *testing.T) {
			derived := evalSetStages(t, pipeline, statusClassDoc(statusClassFixture{polling: "Synchronous", status: v.status}))
			if derived["_statusClass"] != v.want {
				t.Errorf("Mongo pipeline class = %v, want %s", derived["_statusClass"], v.want)
			}
			encoded, err := bson.Marshal(bson.D{{Key: "v", Value: v.status}})
			if err != nil {
				t.Fatal(err)
			}
			if got := legacyStatusClass("synchronous", bson.Raw(encoded).Lookup("v")); got != v.want {
				t.Errorf("legacy class = %s, want %s", got, v.want)
			}
			if text, ok := v.status.(string); ok {
				if got := audit.StatusClass("synchronous", "valid", text); got != v.want {
					t.Errorf("Go class = %s, want %s", got, v.want)
				}
			}
		})
	}
}

// kindScopeDocs are one valid and one incomplete document per derived kind.
func kindScopeDocs() map[string]map[string]any {
	docs := map[string]map[string]any{}
	for name, polling := range map[string]any{"sync": "Synchronous", "async": "Asynchronous", "unknown": "", "hangfire": "Hangfire"} {
		fixture := statusClassFixture{polling: polling, status: "OK"}
		docs[name+"/valid"] = statusClassDoc(fixture)
		docs[name+"/incomplete"] = incompleteDoc(fixture)
	}
	return docs
}

func TestKindHTTPIncludesUnknownExcludesHangfire(t *testing.T) {
	all := []string{"sync/valid", "sync/incomplete", "async/valid", "async/incomplete", "unknown/valid", "unknown/incomplete", "hangfire/valid", "hangfire/incomplete"}
	want := map[string][]string{
		"synchronous":  {"sync/valid", "sync/incomplete", "unknown/incomplete"},
		"asynchronous": {"async/valid", "async/incomplete", "unknown/incomplete"},
		"http":         {"sync/valid", "sync/incomplete", "async/valid", "async/incomplete", "unknown/valid", "unknown/incomplete"},
		"http_unknown": {"unknown/valid", "unknown/incomplete"},
		"hangfire":     {"hangfire/valid", "hangfire/incomplete"},
		"all":          all,
	}
	docs := kindScopeDocs()
	for kind, kept := range want {
		q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
		q.Kind, q.Outcome = kind, "all"
		pipeline := buildPipeline(q, q.Start, q.End)
		wantSet := map[string]bool{}
		for _, name := range kept {
			wantSet[name] = true
		}
		for _, name := range all {
			derived, survives := evalRowSurvives(t, pipeline, docs[name])
			if survives != wantSet[name] {
				t.Errorf("kind=%s %s (derived kind %v, state %v): kept = %v, want %v", kind, name, derived["_auditKind"], derived["_auditState"], survives, wantSet[name])
			}
		}
	}
	// A Hangfire-only incomplete row must never reach an HTTP view, and the
	// pipeline still reports the derived value "unknown" for the HTTP-unknown kind.
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Kind, q.Outcome = "http_unknown", "all"
	derived, _ := evalRowSurvives(t, buildPipeline(q, q.Start, q.End), docs["unknown/valid"])
	if derived["_auditKind"] != "unknown" {
		t.Fatalf("derived kind = %v, want unknown", derived["_auditKind"])
	}
}

func TestRequestPipelineKindScope(t *testing.T) {
	docs := kindScopeDocs()
	names := []string{"sync/valid", "async/valid", "unknown/valid", "hangfire/valid"}
	want := map[string][]string{
		"synchronous":  {"sync/valid"},
		"asynchronous": {"async/valid"},
		"unknown":      {"unknown/valid"},
		"http":         {"sync/valid", "async/valid", "unknown/valid"},
		"http_unknown": {"unknown/valid"},
		"hangfire":     {"hangfire/valid"},
	}
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for kind, kept := range want {
		wantSet := map[string]bool{}
		for _, name := range kept {
			wantSet[name] = true
		}
		pipeline := requestPipeline(q, [5]string{"source", "", "", kind, "all"}, "source", nil, 10)
		for _, name := range names {
			if _, survives := evalRowSurvives(t, pipeline, docs[name]); survives != wantSet[name] {
				t.Errorf("request listing kind=%s %s: kept = %v, want %v", kind, name, survives, wantSet[name])
			}
		}
	}
}
