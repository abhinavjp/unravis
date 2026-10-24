package store

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func allRecordFilters() audit.Query {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q := queryForTest(now)
	q.Kind, q.Outcome = "all", "all"
	q.UserIDs = []int{7, 9}
	q.UIVersions = []string{"2.1", "2.2"}
	q.UserAgentContains = "Chrome"
	q.UserIPs = []string{"10.0.0.1"}
	q.URLContains = "a.b(c"
	q.AuditID = "0123456789abcdef01234567"
	q.StatusCodes = []string{"NotFound", "500"}
	q.Completion = "incomplete"
	return q
}

func matchElem(match bson.D, key string) (any, bool) {
	for _, e := range match {
		if e.Key == key {
			return e.Value, true
		}
	}
	return nil, false
}

func TestRecordMatchEscapesRegex(t *testing.T) {
	q := queryForTest(time.Now().UTC())
	q.URLContains = "a.b(c"
	q.UserAgentContains = "x+y"
	match := recordMatch(q)
	value, ok := matchElem(match, "RequestMainUrl")
	if !ok {
		t.Fatalf("no RequestMainUrl condition in %v", match)
	}
	condition, ok := value.(bson.D)
	if !ok || len(condition) != 2 || condition[0].Key != "$regex" || condition[1].Key != "$options" {
		t.Fatalf("unexpected URL condition %#v", value)
	}
	if condition[0].Value != `a\.b\(c` || condition[1].Value != "i" {
		t.Fatalf("URL regex = %#v option %#v, want a\\.b\\(c and i", condition[0].Value, condition[1].Value)
	}
	agent, _ := matchElem(match, "UserAgent")
	if got := agent.(bson.D)[0].Value; got != `x\+y` {
		t.Fatalf("UserAgent regex = %#v", got)
	}
	if empty := recordMatch(queryForTest(time.Now().UTC())); len(empty) != 0 {
		t.Fatalf("a query without record filters must add nothing, got %v", empty)
	}
}

// walkOperators collects every key that starts with $ in a BSON value.
func walkOperators(v any, found map[string]bool) {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			if strings.HasPrefix(e.Key, "$") {
				found[e.Key] = true
			}
			walkOperators(e.Value, found)
		}
	case bson.A:
		for _, item := range x {
			walkOperators(item, found)
		}
	case mongo.Pipeline:
		for _, stage := range x {
			walkOperators(stage, found)
		}
	}
}

func TestRecordMatchOnly34Operators(t *testing.T) {
	q := allRecordFilters()
	found := map[string]bool{}
	walkOperators(recordMatch(q), found)
	walkOperators(recordPostMatch(q), found)
	for _, banned := range []string{"$regexMatch", "$convert", "$toString", "$dateDiff", "$expr", "$toInt", "$toLong"} {
		if found[banned] {
			t.Errorf("record filter uses %s, which MongoDB 3.4 lacks", banned)
		}
	}
	for op := range found {
		switch op {
		case "$in", "$regex", "$options":
		default:
			t.Errorf("unexpected operator %s in the record filter", op)
		}
	}
	for _, e := range recordMatch(q) {
		switch e.Key {
		case "UserId", "PayrollUIVersion", "UserAgent", "UserIp", "RequestMainUrl", "_id", "StatusCode":
		default:
			t.Errorf("record filter reads %s, not a raw audit field", e.Key)
		}
	}
	id, ok := matchElem(recordMatch(q), "_id")
	if _, isID := id.(bson.ObjectID); !ok || !isID {
		t.Fatalf("AuditID must be matched as an ObjectId, got %#v", id)
	}
}

func firstMatchOf(t *testing.T, pipeline mongo.Pipeline) bson.D {
	t.Helper()
	if len(pipeline) == 0 || pipeline[0][0].Key != "$match" {
		t.Fatalf("pipeline does not start with $match: %v", pipeline)
	}
	return pipeline[0][0].Value.(bson.D)
}

func hasStage(pipeline mongo.Pipeline, want bson.D) bool {
	for i, stage := range pipeline {
		if i > 0 && stage[0].Key == "$match" && reflect.DeepEqual(stage[0].Value, want) {
			return true
		}
	}
	return false
}

func containsElems(match, want bson.D) bool {
	for _, w := range want {
		found := false
		for _, e := range match {
			found = found || (e.Key == w.Key && reflect.DeepEqual(e.Value, w.Value))
		}
		if !found {
			return false
		}
	}
	return true
}

func TestSummaryAndRequestsShareFilter(t *testing.T) {
	q := allRecordFilters()
	raw, post := recordMatch(q), recordPostMatch(q)
	if len(raw) == 0 || len(post) == 0 {
		t.Fatalf("expected both filters, got %v and %v", raw, post)
	}
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	pipelines := map[string]mongo.Pipeline{
		"summary current":   buildSummaryPipeline(q, q.Start, q.End, true),
		"summary reference": buildSummaryPipeline(q, q.BaselineStart, q.BaselineEnd, false),
		"requests":          requestPipeline(q, cohort, "source", nil, 10),
		"requests state":    requestPipelineState(q, cohort, "", "source", nil, 10),
		"slowest":           slowestPipeline(q, "source", nil, nil, 10),
	}
	for name, pipeline := range pipelines {
		if !containsElems(firstMatchOf(t, pipeline), raw) {
			t.Errorf("%s: first $match does not carry recordMatch\n%v", name, firstMatchOf(t, pipeline))
		}
		if !hasStage(pipeline, post) {
			t.Errorf("%s: no $match stage equal to recordPostMatch %v", name, post)
		}
		// The derived filter must run after the stage that defines _auditState.
		defined, matched := -1, -1
		for i, stage := range pipeline {
			if stage[0].Key == "$set" {
				for _, e := range stage[0].Value.(bson.D) {
					if e.Key == "_auditState" {
						defined = i
					}
				}
			}
			if i > 0 && stage[0].Key == "$match" && reflect.DeepEqual(stage[0].Value, post) {
				matched = i
			}
		}
		if defined < 0 || matched < defined {
			t.Errorf("%s: recordPostMatch at stage %d runs before _auditState is derived at %d", name, matched, defined)
		}
	}

	plain := queryForTest(q.End)
	cohort = [5]string{"", "", "", plain.Kind, plain.Outcome}
	for name, pipeline := range map[string]mongo.Pipeline{
		"summary":  buildSummaryPipeline(plain, plain.Start, plain.End, true),
		"requests": requestPipeline(plain, cohort, "source", nil, 10),
	} {
		for _, key := range []string{"UserId", "PayrollUIVersion", "UserAgent", "UserIp", "RequestMainUrl", "_id", "StatusCode"} {
			if _, ok := matchElem(firstMatchOf(t, pipeline), key); ok {
				t.Errorf("%s: an unfiltered query must not match on %s", name, key)
			}
		}
	}
}

func TestPageFingerprintCoversEveryRecordFilter(t *testing.T) {
	base := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	baseline := pageFingerprint(base, "")
	mutations := map[string]func(*audit.Query){
		"user ids":   func(q *audit.Query) { q.UserIDs = []int{7} },
		"versions":   func(q *audit.Query) { q.UIVersions = []string{"2.1"} },
		"agent":      func(q *audit.Query) { q.UserAgentContains = "a" },
		"ips":        func(q *audit.Query) { q.UserIPs = []string{"1.1.1.1"} },
		"url":        func(q *audit.Query) { q.URLContains = "a" },
		"audit id":   func(q *audit.Query) { q.AuditID = "0123456789abcdef01234567" },
		"status":     func(q *audit.Query) { q.StatusCodes = []string{"OK"} },
		"completion": func(q *audit.Query) { q.Completion = "pending" },
		"operation":  func(q *audit.Query) { q.Operation = "A.B" },
	}
	seen := map[string]string{baseline: "none"}
	for name, mutate := range mutations {
		q := base
		mutate(&q)
		fingerprint := pageFingerprint(q, "")
		if prior, dup := seen[fingerprint]; dup {
			t.Errorf("%s fingerprints like %s: a cursor from one filter would page another", name, prior)
		}
		seen[fingerprint] = name
		if slowFingerprint(q, nil) == slowFingerprint(base, nil) {
			t.Errorf("%s does not change the slowest fingerprint", name)
		}
	}
}

func TestLegacyMetadataCarriesTheFilterFields(t *testing.T) {
	for _, field := range []string{"UserId", "UserIp", "PayrollUIVersion", "CreatedOn", "UpdatedOn"} {
		count := 0
		for _, f := range legacyMetadataFields {
			if f == field {
				count++
			}
		}
		if count != 1 {
			t.Errorf("legacyMetadataFields lists %s %d times, want once", field, count)
		}
	}
	for _, fields := range [][]string{legacyMetadataFields, legacyRowMetadataFields} {
		seen := map[string]bool{}
		for _, f := range fields {
			if seen[f] {
				t.Errorf("duplicate projected field %s", f)
			}
			seen[f] = true
		}
	}
	q := allRecordFilters()
	pipeline := legacyMetadataPipeline(buildSummaryPipeline(q, q.Start, q.End, true))
	if !containsElems(firstMatchOf(t, pipeline), recordMatch(q)) {
		t.Fatal("the legacy metadata pipeline lost the raw filter, so a 3.4 server would scan everything")
	}
	for _, e := range pipeline[len(pipeline)-1][0].Value.(bson.D) {
		if e.Key == "UserId" {
			return
		}
	}
	t.Fatal("UserId is not projected")
}

// evalFirstMatch is a test double for the server's evaluation of the raw first
// $match: equality, $in (with regex entries), $regex/$options, $exists, null and
// range conditions, combined with $or and $and.
func evalFirstMatch(t *testing.T, match bson.D, doc map[string]any) bool {
	t.Helper()
	equal := func(have, want any) bool {
		if a, aok := pipelineNum(have); aok {
			b, bok := pipelineNum(want)
			return bok && a == b
		}
		if re, ok := want.(bson.Regex); ok {
			s, isString := have.(string)
			if !isString {
				return false
			}
			pattern := re.Pattern
			if strings.Contains(re.Options, "i") {
				pattern = "(?i)" + pattern
			}
			return regexp.MustCompile(pattern).MatchString(s)
		}
		return reflect.DeepEqual(have, want)
	}
	for _, e := range match {
		switch e.Key {
		case "$or", "$and":
			matched := e.Key == "$and"
			for _, branch := range e.Value.(bson.A) {
				ok := evalFirstMatch(t, branch.(bson.D), doc)
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
		default:
			value, present := doc[e.Key]
			condition, isOps := e.Value.(bson.D)
			if !isOps {
				if e.Value == nil {
					if present && value != nil {
						return false
					}
					continue
				}
				if !present || !equal(value, e.Value) {
					return false
				}
				continue
			}
			var pattern, options string
			for _, op := range condition {
				switch op.Key {
				case "$exists":
					if present != op.Value.(bool) {
						return false
					}
				case "$gte", "$lt":
					at, ok := value.(time.Time)
					bound := op.Value.(time.Time)
					if !present || !ok || (op.Key == "$gte" && at.Before(bound)) || (op.Key == "$lt" && !at.Before(bound)) {
						return false
					}
				case "$in":
					found := false
					for _, item := range op.Value.(bson.A) {
						found = found || (present && equal(value, item))
					}
					if !found {
						return false
					}
				case "$regex":
					pattern = op.Value.(string)
				case "$options":
					options = op.Value.(string)
				default:
					t.Fatalf("unsupported first $match operator %s on %s", op.Key, e.Key)
				}
			}
			if pattern != "" {
				if !present || !equal(value, bson.Regex{Pattern: pattern, Options: options}) {
					return false
				}
			}
		}
	}
	return true
}

type filterFixture struct {
	id      bson.ObjectID
	doc     map[string]any
	user    any
	version any
	timing  string
}

// filterFixtures spans identity, client, URL and status shapes so each record
// filter splits the set. User ids occur as int32, as numeric text and missing.
func filterFixtures(at time.Time) []filterFixture {
	users := []any{int32(7), int32(8), "7", nil}
	versions := []any{"2.1", "2.2", nil}
	timings := []string{"complete", "open"}
	urls := []string{"/api/Payroll/Run?a.b(c", "/api/Other"}
	agents := []string{"Mozilla/5.0 Chrome", "curl/8.0"}
	ips := []string{"10.0.0.1", "10.0.0.2"}
	statuses := []any{"OK", "NotFound", int32(404), "500"}
	var out []filterFixture
	n := 0
	for _, user := range users {
		for _, version := range versions {
			for _, timing := range timings {
				for _, url := range urls {
					for _, agent := range agents {
						for _, ip := range ips {
							for _, status := range statuses {
								doc := map[string]any{
									"RequestPollingType": "Synchronous ", "StatusCode": status, "RequestInitiatedAt": at,
									"ControllerName": "Payroll", "ActionName": []string{"Run", "Other"}[(n/7)%2], "_auditMetadataOversized": false,
									"RequestMainUrl": url, "UserAgent": agent, "UserIp": ip,
								}
								if user != nil {
									doc["UserId"] = user
								}
								if version != nil {
									doc["PayrollUIVersion"] = version
								}
								if timing == "complete" {
									doc["RequestCompletedAt"], doc["ApiTime"] = at.Add(500*time.Millisecond), 480.0
								}
								id := bson.NewObjectID()
								doc["_id"] = id
								out = append(out, filterFixture{id: id, doc: doc, user: user, version: version, timing: timing})
								n++
							}
						}
					}
				}
			}
		}
	}
	return out
}

func modernFilteredIDs(t *testing.T, pipeline mongo.Pipeline, fixtures []filterFixture) []string {
	t.Helper()
	var ids []string
	for _, f := range fixtures {
		if !evalFirstMatch(t, firstMatchOf(t, pipeline), f.doc) {
			continue
		}
		if _, kept := evalRowSurvives(t, pipeline, f.doc); kept {
			ids = append(ids, f.id.Hex())
		}
	}
	sort.Strings(ids)
	return ids
}

func legacyFilteredRows(t *testing.T, p *profile, handle *fakeClient, q audit.Query, fixtures []filterFixture) []string {
	t.Helper()
	raws := make([]any, 0, len(fixtures))
	for _, f := range fixtures {
		raws = append(raws, drillRaw(t, f.doc))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var records, rawBytes atomic.Int64
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	rows, err := readLegacyRequestRowsState(context.Background(), p, q, cohort, "", nil, nil, 100000, &records, &rawBytes, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.request.ID)
	}
	sort.Strings(ids)
	return ids
}

func legacySummaryCount(t *testing.T, p *profile, handle *fakeClient, q audit.Query, fixtures []filterFixture) int64 {
	t.Helper()
	raws := make([]any, 0, len(fixtures))
	for _, f := range fixtures {
		raws = append(raws, drillRaw(t, f.doc))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var rows, bytes, records, rawBytes atomic.Int64
	buckets, err := readLegacyBuckets(context.Background(), p, q, q.Start, nil, &rows, &bytes, &records, &rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	for _, b := range buckets {
		count += b.Count
	}
	return count
}

func modernSummaryCount(t *testing.T, q audit.Query, fixtures []filterFixture) int64 {
	t.Helper()
	pipeline := buildSummaryPipeline(q, q.Start, q.End, true)
	return int64(len(modernFilteredIDs(t, pipeline, fixtures)))
}

func TestLegacyFiltersMatchModern(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	at := now.Add(-5 * time.Minute)
	fixtures := filterFixtures(at)
	p, handle, release := legacyProfile(t)
	defer release()

	q := queryForTest(now)
	q.Kind, q.Outcome = "all", "all"
	q.UserIDs, q.Completion, q.UIVersions = []int{7}, "incomplete", []string{"2.1"}

	want := int64(0)
	for _, f := range fixtures {
		user := f.user == int32(7) || f.user == "7"
		if user && f.version == "2.1" && f.timing == "open" {
			want++
		}
	}
	if want == 0 || want == int64(len(fixtures)) {
		t.Fatalf("the brief's filter must split the fixtures, matched %d of %d", want, len(fixtures))
	}
	if got := modernSummaryCount(t, q, fixtures); got != want {
		t.Fatalf("modern summary counted %d, want %d", got, want)
	}
	if got := legacySummaryCount(t, p, handle, q, fixtures); got != want {
		t.Fatalf("legacy summary counted %d, want %d", got, want)
	}
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	modernIDs := modernFilteredIDs(t, requestPipelineState(q, cohort, "", "source", nil, 100000), fixtures)
	legacyIDs := legacyFilteredRows(t, p, handle, q, fixtures)
	if int64(len(modernIDs)) != want || !sameIDs(legacyIDs, modernIDs) {
		t.Fatalf("request rows differ: modern %d legacy %d want %d", len(modernIDs), len(legacyIDs), want)
	}
}

func TestEveryRecordFilterAgreesOnBothPaths(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	fixtures := filterFixtures(now.Add(-5 * time.Minute))
	p, handle, release := legacyProfile(t)
	defer release()
	base := queryForTest(now)
	base.Kind, base.Outcome = "all", "all"
	pickID := fixtures[37].id.Hex()
	cases := map[string]func(*audit.Query){
		"user ids":           func(q *audit.Query) { q.UserIDs = []int{7, 8} },
		"ui versions":        func(q *audit.Query) { q.UIVersions = []string{"2.2"} },
		"agent ci":           func(q *audit.Query) { q.UserAgentContains = "CHROME" },
		"ips":                func(q *audit.Query) { q.UserIPs = []string{"10.0.0.2"} },
		"url ci":             func(q *audit.Query) { q.URLContains = "payroll/RUN" },
		"url regex chars":    func(q *audit.Query) { q.URLContains = "a.b(c" },
		"audit id":           func(q *audit.Query) { q.AuditID = pickID },
		"status name ci":     func(q *audit.Query) { q.StatusCodes = []string{"notfound"} },
		"status numeric":     func(q *audit.Query) { q.StatusCodes = []string{"404"} },
		"status many":        func(q *audit.Query) { q.StatusCodes = []string{"OK", "500"} },
		"completion":         func(q *audit.Query) { q.Completion = "complete" },
		"completion pending": func(q *audit.Query) { q.Completion = "incomplete" },
		"operation":          func(q *audit.Query) { q.Operation = "Payroll.Run" },
		"operation miss":     func(q *audit.Query) { q.Operation = "Payroll.Missing" },
		"combined":           func(q *audit.Query) { q.UserIDs = []int{7}; q.URLContains = "other"; q.StatusCodes = []string{"OK"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := base
			mutate(&q)
			effective := q.Normalized()
			cohort := [5]string{"", effective.Controller, effective.Action, effective.Kind, effective.Outcome}
			modern := modernFilteredIDs(t, requestPipelineState(effective, cohort, "", "source", nil, 100000), fixtures)
			legacy := legacyFilteredRows(t, p, handle, effective, fixtures)
			if !sameIDs(legacy, modern) {
				t.Fatalf("legacy returned %d rows, modern %d", len(legacy), len(modern))
			}
			if name != "operation miss" && (len(modern) == 0 || len(modern) == len(fixtures)) {
				t.Fatalf("filter matched %d of %d, so it proves nothing", len(modern), len(fixtures))
			}
			if name == "operation miss" && len(modern) != 0 {
				t.Fatalf("a different action matched %d rows", len(modern))
			}
			if got, want := legacySummaryCount(t, p, handle, effective, fixtures), modernSummaryCount(t, effective, fixtures); got != want {
				t.Fatalf("summary counts differ: legacy %d modern %d", got, want)
			}
			if name == "audit id" && len(modern) != 1 {
				t.Fatalf("an audit id selects one row, got %d", len(modern))
			}
		})
	}
}

func TestOperationFilterReachesThePipelineOnce(t *testing.T) {
	var handle *fakeClient
	var captured []mongo.Pipeline
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		captured = append(captured, pipeline)
		return &fakeCursor{}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	q.Operation = "Payroll.Run.Now"
	analysis, err := s.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Query.Operation != "Payroll.Run.Now" {
		t.Fatalf("the analysis must echo the query as sent, got %q", analysis.Query.Operation)
	}
	if len(captured) == 0 {
		t.Fatal("no pipeline ran")
	}
	for _, pipeline := range captured {
		match := firstMatchOf(t, pipeline)
		controllers, actions := 0, 0
		for _, e := range match {
			switch e.Key {
			case "ControllerName":
				controllers++
				if e.Value != "Payroll" {
					t.Errorf("controller = %v", e.Value)
				}
			case "ActionName":
				actions++
				if e.Value != "Run.Now" {
					t.Errorf("action = %v", e.Value)
				}
			}
		}
		if controllers != 1 || actions != 1 {
			t.Errorf("first $match has %d controller and %d action conditions: %v", controllers, actions, match)
		}
	}
	captured = nil
	page, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sources) != 1 || !page.Sources[0].OK || len(captured) != 1 {
		t.Fatalf("requests did not run: %#v", page.Sources)
	}
	if v, _ := matchElem(firstMatchOf(t, captured[0]), "ControllerName"); v != "Payroll" {
		t.Fatalf("requests ignored the operation: %v", firstMatchOf(t, captured[0]))
	}
	if cursor := fmt.Sprint(page.NextCursor); cursor != "" {
		t.Fatalf("unexpected cursor %q", cursor)
	}
}
