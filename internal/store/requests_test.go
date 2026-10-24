package store

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestRequestsRejectsBadPageAndFingerprintMismatch(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: audit.MaxPageSize + 1}); !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("oversize page error = %v", err)
	}
	badCursor, _ := json.Marshal(pageCursor{At: time.Now().UTC(), SourceID: source.ID, ObjectID: bson.NewObjectID().Hex(), Fingerprint: "wrong"})
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, Cursor: base64.RawURLEncoding.EncodeToString(badCursor), Limit: 10}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("fingerprint error = %v", err)
	}
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, APIKey: audit.CohortKey("other", "Payroll", "Run", "synchronous", "success"), Limit: 10}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("API key source mismatch = %v", err)
	}
}

func TestRequestsPaginatesEqualTimestampsAcrossSourcesWithoutDuplicates(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	sourceA := addFakeSource(t, s, "audit-a")
	sourceB := addFakeSource(t, s, "audit-b")
	now := time.Now().UTC().Truncate(time.Second)
	id := func(hex string) bson.ObjectID { result, _ := bson.ObjectIDFromHex(hex); return result }
	all := map[string][]requestDocument{
		"audit-a": {{ID: id("000000000000000000000002"), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &now}, {ID: id("000000000000000000000001"), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &now}},
		"audit-b": {{ID: id("000000000000000000000004"), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &now}, {ID: id("000000000000000000000003"), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &now}},
	}
	var after *pageCursor
	handle.aggregateFn = func(_ context.Context, _, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		var rows []pageRow
		for _, doc := range all[collection] {
			rows = append(rows, pageRow{request: audit.RequestRecord{SourceID: collectionSourceID(collection, sourceA, sourceB), Controller: doc.Controller, Action: doc.Action, Kind: doc.Kind, Outcome: doc.Outcome}, at: now, object: doc.ID})
		}
		_ = pipeline
		sort.Slice(rows, func(i, j int) bool { return pageRowBefore(rows[i], rows[j]) })
		filtered := rows[:0]
		for _, row := range rows {
			if after == nil {
				filtered = append(filtered, row)
				continue
			}
			cursorRow := pageRow{request: audit.RequestRecord{SourceID: after.SourceID}, at: after.At, object: mustObjectID(after.ObjectID)}
			if pageRowBefore(cursorRow, row) {
				filtered = append(filtered, row)
			}
		}
		if len(filtered) > 3 {
			filtered = filtered[:3]
		}
		docs := make([]any, len(filtered))
		for i, row := range filtered {
			docs[i] = requestDocument{ID: row.object, Controller: row.request.Controller, Action: row.request.Action, Kind: row.request.Kind, Outcome: row.request.Outcome, InitiatedAt: &row.at}
		}
		return &fakeCursor{docs: docs}, nil
	}
	q := queryForTest(now)
	q.SourceIDs = []string{sourceB.ID, sourceA.ID}
	q.Kind, q.Outcome = "all", "all"
	first, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	firstSource, secondSource := sourceA.ID, sourceB.ID
	if secondSource < firstSource {
		firstSource, secondSource = secondSource, firstSource
	}
	if first.Rows[0].SourceID != firstSource || first.Rows[1].SourceID != firstSource {
		t.Fatalf("source tie order = %#v", first.Rows)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	var cursor pageCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	after = &cursor
	second, err := s.Requests(context.Background(), audit.PageInput{Query: q, Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 2 || second.Rows[0].SourceID != secondSource || second.Rows[1].SourceID != secondSource {
		t.Fatalf("second page = %#v, expected %s; A=%s B=%s", second.Rows, secondSource, sourceA.ID, sourceB.ID)
	}
	seen := map[string]bool{}
	for _, row := range append(first.Rows, second.Rows...) {
		if seen[row.ID] {
			t.Fatalf("duplicate row %s", row.ID)
		}
		seen[row.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("got %d rows, want 4", len(seen))
	}
}

func TestRequestPipelineUsesMetadataOnlyAndCursorTieFields(t *testing.T) {
	now := time.Now().UTC()
	q := queryForTest(now)
	q.SourceIDs = []string{"src"}
	q.Kind, q.Outcome = "all", "all"
	cohort := [5]string{"", "", "", "all", "all"}
	cursor := &pageCursor{At: now, SourceID: "a", ObjectID: "000000000000000000000001"}
	pipeline := requestPipeline(q, cohort, "a", cursor, 101)
	b, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"RequestInitiatedAt", "CreatedOn", "RequestCompletedAt", "RequestMainUrl", "StatusCode", "$convert", "RequestLengthInKiloBytes", "ResponseLengthInKiloBytes", "$sort", "$limit", "000000000000000000000001"} {
		if !strings.Contains(text, want) {
			t.Errorf("request pipeline missing %q", want)
		}
	}
	for _, forbidden := range []string{"RequestPayload", "ResponsePayload", "$push"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("request pipeline contains %q", forbidden)
		}
	}
}

func TestRequestsReportsDecodeFailureInsteadOfSuccess(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{bucketDocument{}}}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	page, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sources) != 1 || page.Sources[0].OK || page.Sources[0].Error == "" {
		t.Fatalf("decode failure was not reported: %#v", page.Sources)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("decode failure returned partial rows: %#v", page.Rows)
	}
}

func collectionSourceID(collection string, a, b audit.Source) string {
	if collection == "audit-a" {
		return a.ID
	}
	return b.ID
}
func mustObjectID(value string) bson.ObjectID {
	result, _ := bson.ObjectIDFromHex(value)
	return result
}

func TestUnknownKindCohortDrillableUnderSynchronousFilter(t *testing.T) {
	q := queryForTest(time.Now().UTC())
	q.SourceIDs = []string{"src"}
	q.Kind, q.Outcome = "synchronous", "success"
	// An incomplete row without a polling type has kind unknown and outcome
	// unknown; the summary keeps it under a synchronous filter, so its cohort
	// must open.
	if err := validateCohortQuery(q, [5]string{"src", "c", "a", "unknown", "unknown"}); err != nil {
		t.Fatalf("unknown-kind cohort under a synchronous filter: %v", err)
	}
	q.Kind = "asynchronous"
	if err := validateCohortQuery(q, [5]string{"src", "c", "a", "unknown", "unknown"}); err != nil {
		t.Fatalf("unknown-kind cohort under an asynchronous filter: %v", err)
	}
	for _, kind := range []string{"synchronous", "asynchronous", "http", "all"} {
		for _, outcome := range []string{"all", "success", "failure", "client_error", "server_error"} {
			q.Kind, q.Outcome = kind, outcome
			if err := validateCohortQuery(q, [5]string{"src", "c", "a", "unknown", "unknown"}); err != nil {
				t.Errorf("unknown/unknown cohort under %s/%s: %v", kind, outcome, err)
			}
		}
	}
}

func TestHangfireCohortRejectedUnderSynchronousFilter(t *testing.T) {
	q := queryForTest(time.Now().UTC())
	q.SourceIDs = []string{"src"}
	q.Kind, q.Outcome = "synchronous", "success"
	if err := validateCohortQuery(q, [5]string{"src", "c", "a", "hangfire", "success"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("hangfire cohort under a synchronous filter = %v, want ErrInvalidInput", err)
	}
	q.Kind = "hangfire"
	if err := validateCohortQuery(q, [5]string{"src", "c", "a", "unknown", "unknown"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("unknown-kind cohort under a hangfire filter = %v, want ErrInvalidInput", err)
	}
	q.Kind, q.Outcome = "all", "server_error"
	if err := validateCohortQuery(q, [5]string{"src", "c", "a", "synchronous", "success"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("success cohort under server_error = %v, want ErrInvalidInput", err)
	}
}

func TestRequestsStateFilterIncomplete(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	var pipelines []mongo.Pipeline
	handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		pipelines = append(pipelines, pipeline)
		return &fakeCursor{}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs, q.Kind, q.Outcome = []string{source.ID}, "all", "all"
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, State: "incomplete", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, State: "all", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if len(pipelines) != 2 {
		t.Fatalf("pipelines = %d, want 2", len(pipelines))
	}
	text := func(p mongo.Pipeline) string {
		b, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: p}}, false, false)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := text(pipelines[0]); !strings.Contains(got, `{"_auditState":"incomplete"}`) || !strings.Contains(got, `"state":"$_auditState"`) {
		t.Errorf("incomplete drill-down pipeline has no state match or projection: %s", got)
	}
	if got := text(pipelines[1]); strings.Contains(got, `{"_auditState":"incomplete"}`) {
		t.Errorf("state=all must not filter: %s", got)
	}
	if _, err := s.Requests(context.Background(), audit.PageInput{Query: q, State: "bogus", Limit: 10}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Errorf("unknown state error = %v, want ErrInvalidInput", err)
	}
	// A cursor minted for one state must not page another.
	cursor, _ := json.Marshal(pageCursor{At: time.Now().UTC(), SourceID: source.ID, ObjectID: bson.NewObjectID().Hex(), Fingerprint: pageFingerprint(q, "")})
	input := audit.PageInput{Query: q, State: "incomplete", Cursor: base64.RawURLEncoding.EncodeToString(cursor), Limit: 10}
	if _, err := s.Requests(context.Background(), input); !errors.Is(err, audit.ErrInvalidInput) {
		t.Errorf("cursor from another state error = %v, want ErrInvalidInput", err)
	}
}

func orderTestStore(t *testing.T) (*Store, *fakeClient, audit.Source) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{}, nil
	}
	return s, handle, source
}

func TestOldestFirstRejectedWithoutIncompleteFilter(t *testing.T) {
	s, _, source := orderTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	min := 600000.0
	cases := []struct {
		name   string
		order  string
		mutate func(*audit.Query)
		reject bool
	}{
		{"oldest with no completion filter", "oldest", func(*audit.Query) {}, true},
		{"oldest with completion all", "oldest", func(q *audit.Query) { q.Completion = "all" }, true},
		{"oldest with completion complete", "oldest", func(q *audit.Query) { q.Completion = "complete" }, true},
		{"oldest with completion invalid", "oldest", func(q *audit.Query) { q.Completion = "invalid" }, true},
		{"oldest with only a latency range", "oldest", func(q *audit.Query) { q.ObservedMs = &audit.Range{Min: &min} }, true},
		{"unknown order", "sideways", func(q *audit.Query) { q.Completion = "incomplete" }, true},
		{"oldest with completion incomplete", "oldest", func(q *audit.Query) { q.Completion = "incomplete" }, false},
		{"oldest with completion pending", "oldest", func(q *audit.Query) { q.Completion = "pending" }, false},
		{"oldest with only an age range", "oldest", func(q *audit.Query) { q.IncompleteAgeMs = &audit.Range{Min: &min} }, false},
		{"default order needs no filter", "", func(*audit.Query) {}, false},
		{"newest needs no filter", "newest", func(*audit.Query) {}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := queryForTest(now)
			q.SourceIDs = []string{source.ID}
			q.Kind, q.Outcome = "all", "all"
			c.mutate(&q)
			_, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: 10, Order: c.order})
			if c.reject && !errors.Is(err, audit.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}
			if !c.reject && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOldestFirstIsIgnoredBySlowestAndActivityLists(t *testing.T) {
	// Only the request history takes an order; the slowest and activity lists rank by their own keys.
	s, _, source := orderTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := queryForTest(now)
	q.SourceIDs = []string{source.ID}
	q.Kind, q.Outcome = "all", "all"
	if _, err := s.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 10, Order: "oldest"}); err != nil {
		t.Fatalf("slowest must ignore the order: %v", err)
	}
}

func TestOldestPipelineSortsAscendingAndKeysetMirrorsNewest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q := queryForTest(now)
	q.SourceIDs = []string{"a", "b", "c"}
	q.Kind, q.Outcome, q.Completion = "all", "all", "incomplete"
	cohort := [5]string{"", "", "", "all", "all"}
	id := func(n int) bson.ObjectID { return mustObjectID(fmt.Sprintf("%024x", n)) }
	t1, t2, t3 := now.Add(-3*time.Minute), now.Add(-2*time.Minute), now.Add(-time.Minute)
	type row struct {
		at time.Time
		id bson.ObjectID
	}
	rows := []row{{t1, id(5)}, {t2, id(4)}, {t2, id(6)}, {t3, id(1)}}
	cursor := &pageCursor{At: t2, SourceID: "b", ObjectID: id(5).Hex()}
	// Independent statement of "after the cursor": oldest walks (at, sourceId, _id) upward, newest downward on at and _id.
	after := func(oldest bool, source string, r row) bool {
		cmp := 0
		switch {
		case r.at.After(cursor.At):
			cmp = 1
		case r.at.Before(cursor.At):
			cmp = -1
		case source > cursor.SourceID:
			return true
		case source < cursor.SourceID:
			return false
		case r.id.Hex() > cursor.ObjectID:
			cmp = 1
		case r.id.Hex() < cursor.ObjectID:
			cmp = -1
		}
		if oldest {
			return cmp > 0
		}
		return cmp < 0
	}
	for _, oldest := range []bool{true, false} {
		for _, source := range []string{"a", "b", "c"} {
			pipeline := requestPipelineOrdered(q, cohort, "", true, oldest, source, cursor, 11)
			var sortStage bson.D
			for _, stage := range pipeline {
				if stage[0].Key == "$sort" {
					sortStage = stage[0].Value.(bson.D)
				}
			}
			wantDirection := -1
			if oldest {
				wantDirection = 1
			}
			if len(sortStage) != 2 || sortStage[0].Key != "_auditAt" || sortStage[1].Key != "_id" || sortStage[0].Value != wantDirection || sortStage[1].Value != wantDirection {
				t.Fatalf("oldest=%v sort = %#v, want _auditAt and _id both %d", oldest, sortStage, wantDirection)
			}
			for _, r := range rows {
				doc := map[string]any{
					"_id": r.id, "RequestPollingType": "Synchronous ", "StatusCode": "", "RequestInitiatedAt": r.at,
					"ControllerName": "Payroll", "ActionName": "Run", "_auditMetadataOversized": false,
				}
				_, kept := evalRowSurvives(t, pipeline, doc)
				if kept != after(oldest, source, r) {
					t.Errorf("oldest=%v source=%s row at=%s id=%s: kept=%v, want %v", oldest, source, r.at.Format(time.TimeOnly), r.id.Hex()[20:], kept, after(oldest, source, r))
				}
			}
		}
	}
}

func TestLegacyOldestFirstPagesStablyAcrossEqualTimestamps(t *testing.T) {
	p, handle, release := legacyProfile(t)
	defer release()
	now := time.Now().UTC().Truncate(time.Second)
	q := queryForTest(now)
	q.SourceIDs = []string{p.source.ID}
	q.Kind, q.Outcome, q.Completion = "all", "all", "incomplete"
	cohort := [5]string{"", "", "", "all", "all"}
	times := []time.Time{now.Add(-9 * time.Minute), now.Add(-9 * time.Minute), now.Add(-9 * time.Minute), now.Add(-7 * time.Minute), now.Add(-7 * time.Minute), now.Add(-5 * time.Minute), now.Add(-3 * time.Minute)}
	raws := make([]any, 0, len(times))
	type fact struct {
		at time.Time
		id string
	}
	var want []fact
	for i, at := range times {
		id := mustObjectID(fmt.Sprintf("%024x", 100-i*7))
		raws = append(raws, drillRaw(t, map[string]any{
			"_id": id, "RequestPollingType": "Synchronous ", "StatusCode": "", "RequestInitiatedAt": at,
			"ControllerName": "Payroll", "ActionName": "Run", "_auditMetadataOversized": false,
		}))
		want = append(want, fact{at, id.Hex()})
	}
	// An already completed request must never show up in an incomplete list.
	raws = append(raws, drillRaw(t, map[string]any{
		"_id": mustObjectID(fmt.Sprintf("%024x", 999)), "RequestPollingType": "Synchronous ", "StatusCode": "OK", "RequestInitiatedAt": times[0],
		"RequestCompletedAt": times[0].Add(time.Second), "ApiTime": 900.0, "ControllerName": "Payroll", "ActionName": "Run", "_auditMetadataOversized": false,
	}))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	sort.Slice(want, func(i, j int) bool {
		if !want[i].at.Equal(want[j].at) {
			return want[i].at.Before(want[j].at)
		}
		return want[i].id < want[j].id
	})
	for _, oldest := range []bool{true, false} {
		expected := append([]fact(nil), want...)
		if !oldest {
			for i, j := 0, len(expected)-1; i < j; i, j = i+1, j-1 {
				expected[i], expected[j] = expected[j], expected[i]
			}
		}
		var got []string
		var after *pageCursor
		for pages := 0; pages < 10; pages++ {
			var records, rawBytes atomic.Int64
			rows, err := readLegacyRequestRowsOrdered(context.Background(), p, q, cohort, "", after, nil, 3, &records, &rawBytes, false, nil, oldest)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				got = append(got, row.request.ID)
			}
			last := rows[len(rows)-1]
			after = &pageCursor{At: last.at, SourceID: last.request.SourceID, ObjectID: last.object.Hex()}
		}
		if len(got) != len(expected) {
			t.Fatalf("oldest=%v paged %d rows, want %d: %v", oldest, len(got), len(expected), got)
		}
		for i := range expected {
			if got[i] != expected[i].id {
				t.Fatalf("oldest=%v row %d = %s, want %s (all %v)", oldest, i, got[i], expected[i].id, got)
			}
		}
	}
}

func TestInsertPageRowOrderedKeepsTheOldestRowsAndBreaksTiesByID(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	var rows []pageRow
	for i, at := range []time.Time{base, base.Add(-time.Minute), base, base.Add(time.Minute), base} {
		insertPageRowOrdered(&rows, pageRow{at: at, object: mustObjectID(fmt.Sprintf("%024x", 10-i))}, 4, true)
	}
	if len(rows) != 4 {
		t.Fatalf("kept %d rows, want 4", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.at.After(b.at) || (a.at.Equal(b.at) && a.object.Hex() >= b.object.Hex()) {
			t.Fatalf("rows are not oldest first with ascending ids: %v then %v", a, b)
		}
	}
	if !rows[0].at.Equal(base.Add(-time.Minute)) {
		t.Fatalf("the oldest row must come first: %v", rows[0])
	}
}

func orderedPageFixture(t *testing.T) (*Store, audit.Query, func(string) audit.PageInput) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	a, b := addFakeSource(t, s, "audit-a"), addFakeSource(t, s, "audit-b")
	now := time.Now().UTC().Truncate(time.Second)
	at := func(minutes int) *time.Time { value := now.Add(time.Duration(-minutes) * time.Minute); return &value }
	id := func(n int) bson.ObjectID { return mustObjectID(fmt.Sprintf("%024x", n)) }
	docs := map[string][]requestDocument{
		"audit-a": {{ID: id(1), InitiatedAt: at(9)}, {ID: id(3), InitiatedAt: at(5)}},
		"audit-b": {{ID: id(2), InitiatedAt: at(7)}, {ID: id(4), InitiatedAt: at(3)}},
	}
	handle.aggregateFn = func(_ context.Context, _, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		// Each source answers its own oldest-first order; the descending order is its reverse.
		ordered := append([]requestDocument(nil), docs[collection]...)
		for _, stage := range pipeline {
			if stage[0].Key == "$sort" && stage[0].Value.(bson.D)[0].Value == -1 {
				for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
					ordered[i], ordered[j] = ordered[j], ordered[i]
				}
			}
		}
		out := make([]any, len(ordered))
		for i, doc := range ordered {
			doc.Controller, doc.Action, doc.Kind, doc.Outcome, doc.State = "Payroll", "Run", "synchronous", "unknown", "incomplete"
			out[i] = doc
		}
		return &fakeCursor{docs: out}, nil
	}
	q := queryForTest(now)
	q.SourceIDs = []string{a.ID, b.ID}
	q.Kind, q.Outcome, q.Completion = "all", "all", "incomplete"
	return s, q, func(order string) audit.PageInput { return audit.PageInput{Query: q, Limit: 2, Order: order} }
}

func TestOldestFirstMergesSourcesAscendingAndBindsCursorToOrder(t *testing.T) {
	s, _, input := orderedPageFixture(t)
	ctx := context.Background()
	oldest, err := s.Requests(ctx, input("oldest"))
	if err != nil {
		t.Fatal(err)
	}
	if len(oldest.Rows) != 2 || oldest.NextCursor == "" {
		t.Fatalf("oldest page = %#v", oldest)
	}
	if !oldest.Rows[0].InitiatedAt.Before(*oldest.Rows[1].InitiatedAt) {
		t.Fatalf("oldest page is not ascending: %v then %v", oldest.Rows[0].InitiatedAt, oldest.Rows[1].InitiatedAt)
	}
	newest, err := s.Requests(ctx, input("newest"))
	if err != nil {
		t.Fatal(err)
	}
	if len(newest.Rows) != 2 || !newest.Rows[0].InitiatedAt.After(*newest.Rows[1].InitiatedAt) {
		t.Fatalf("newest page is not descending: %#v", newest.Rows)
	}
	if oldest.Rows[0].ID == newest.Rows[0].ID {
		t.Fatalf("both orders started with the same row %s", oldest.Rows[0].ID)
	}
	// A cursor belongs to the order that made it.
	withCursor := func(order, cursor string) error {
		in := input(order)
		in.Cursor = cursor
		_, err := s.Requests(ctx, in)
		return err
	}
	if err := withCursor("oldest", oldest.NextCursor); err != nil {
		t.Fatalf("an oldest cursor must continue an oldest listing: %v", err)
	}
	if err := withCursor("newest", oldest.NextCursor); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("an oldest cursor under newest = %v, want invalid input", err)
	}
	if err := withCursor("oldest", newest.NextCursor); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("a newest cursor under oldest = %v, want invalid input", err)
	}
	// An empty order is the newest order, so its cursors are interchangeable.
	if err := withCursor("", newest.NextCursor); err != nil {
		t.Fatalf("a newest cursor must continue a default-order listing: %v", err)
	}
}

func TestRequestsCarryPageProvenanceInExactMode(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, nil)
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome = []string{f.source.ID}, "all", "all"
	page, err := f.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if with, without := f.idMatches(); with != 0 || without != 1 || f.calibrations() != 0 {
		t.Fatalf("exact must add no _id stage (with=%d without=%d cal=%d)", with, without, f.calibrations())
	}
	if len(page.Provenance) != 1 {
		t.Fatalf("provenance = %+v", page.Provenance)
	}
	pv := page.Provenance[0]
	if pv.SourceID != f.source.ID || pv.Window != "page" || pv.QueryMode != audit.QueryModeExact || pv.Completeness != audit.RanToEnd || pv.DocsExamined != nil || pv.PartialReason != "" {
		t.Fatalf("pv = %+v", pv)
	}
	if !pv.Start.Equal(q.Start) || !pv.End.Equal(q.End) {
		t.Fatalf("window = %v..%v", pv.Start, pv.End)
	}
}

func TestRequestsHintPrependsIDStageAndLabels(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, calibrationDocs(t, now.Add(-2*time.Hour), 1000))
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome, q.QueryMode = []string{f.source.ID}, "all", "all", audit.QueryModeHint
	for name, call := range map[string]func() (audit.Page, error){
		"requests": func() (audit.Page, error) {
			return f.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
		},
		"slowest": func() (audit.Page, error) {
			return f.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 5})
		},
	} {
		f.pipelines = nil
		page, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if with, without := f.idMatches(); with != 1 || without != 0 {
			t.Fatalf("%s: hint must prepend one _id stage (with=%d without=%d)", name, with, without)
		}
		if len(page.Provenance) != 1 || page.Provenance[0].Completeness != audit.HintBounded || page.Provenance[0].Margin == nil || page.Provenance[0].Window != "page" {
			t.Fatalf("%s: provenance = %+v", name, page.Provenance)
		}
	}
}

func TestRequestsHintFallsBackWithNoteWhenCalibrationFails(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, nil)
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome, q.QueryMode = []string{f.source.ID}, "all", "all", audit.QueryModeHint
	page, err := f.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if with, _ := f.idMatches(); with != 0 {
		t.Fatal("a failed calibration must run exact")
	}
	pv := page.Provenance[0]
	if pv.QueryMode != audit.QueryModeExact || pv.Margin != nil || !strings.HasPrefix(pv.Note, "hint unavailable: ") {
		t.Fatalf("pv = %+v", pv)
	}
}

func TestActivityRunsExactEvenWhenHintIsAsked(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, calibrationDocs(t, now.Add(-2*time.Hour), 1000))
	// Activity carries no query mode, so it is exact.
	page, err := f.store.Activity(context.Background(), audit.ActivityInput{SourceIDs: []string{f.source.ID}, Start: now.Add(-time.Hour), End: now, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if with, without := f.idMatches(); with != 0 || without != 1 || f.calibrations() != 0 {
		t.Fatalf("activity must run exact (with=%d without=%d cal=%d)", with, without, f.calibrations())
	}
	if pv := page.Provenance[0]; pv.QueryMode != audit.QueryModeExact || pv.Note != "" {
		t.Fatalf("pv = %+v", pv)
	}
	// Activity includes requests that began before the window, which an _id bound
	// over the window would drop. Even if a hint query reaches the overlap reader,
	// it runs exact and says so.
	f.pipelines = nil
	q, err := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{f.source.ID}, Start: now.Add(-time.Hour), End: now, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	q.QueryMode = audit.QueryModeHint
	page, err = f.store.requests(context.Background(), audit.PageInput{Query: q, Limit: 5}, true)
	if err != nil {
		t.Fatal(err)
	}
	if with, without := f.idMatches(); with != 0 || without != 1 || f.calibrations() != 0 {
		t.Fatalf("overlap must run exact (with=%d without=%d cal=%d)", with, without, f.calibrations())
	}
	if pv := page.Provenance[0]; pv.QueryMode != audit.QueryModeExact || !strings.HasPrefix(pv.Note, "hint unavailable: ") {
		t.Fatalf("pv = %+v", pv)
	}
}

func TestRequestsHintOnLegacyRunsExactWithNote(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, true, nil)
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome, q.QueryMode = []string{f.source.ID}, "all", "all", audit.QueryModeHint
	page, err := f.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if with, _ := f.idMatches(); with != 0 || f.calibrations() != 0 {
		t.Fatal("legacy must not use hint")
	}
	if len(page.Provenance) != 1 || !strings.Contains(page.Provenance[0].Note, "not available on MongoDB 3.4") {
		t.Fatalf("provenance = %+v", page.Provenance)
	}
}

func TestRequestsTruncatedProvenanceRowcap(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	a := addFakeSource(t, s, "one")
	b := addFakeSource(t, s, "two")
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		duration := 100.
		return &fakeCursor{docs: []any{requestDocument{ID: bson.NewObjectID(), InitiatedAt: &at, DurationMs: &duration}}}, nil
	}
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{a.ID, b.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	page, err := s.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated || len(page.Provenance) != 2 {
		t.Fatalf("page = %+v", page)
	}
	for _, pv := range page.Provenance {
		if pv.Completeness != audit.Partial || pv.PartialReason != "rowcap" || pv.DocsReturned != 1 {
			t.Fatalf("pv = %+v", pv)
		}
	}
}

func TestRequestsTimeoutProvenancePartial(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s, source, handle := recordStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, context.DeadlineExceeded
	}
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome = []string{source.ID}, "all", "all"
	page, err := s.Requests(context.Background(), audit.PageInput{Query: q, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Provenance) != 1 || page.Provenance[0].PartialReason != "timeout" || page.Provenance[0].Completeness != audit.Partial {
		t.Fatalf("provenance = %+v", page.Provenance)
	}
}
