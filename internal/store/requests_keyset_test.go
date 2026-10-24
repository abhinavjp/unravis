package store

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// keysetFixture is a window of raw audit documents that exercises every way a
// row can reach the window match: a start time, a missing or null start time
// with a creation time, ties on one timestamp, rows on the window edges, rows
// outside it, and a start time that is not a date.
type keysetFixture struct {
	q    audit.Query
	docs []map[string]any
}

func keysetFixtureDocs() keysetFixture {
	end := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	q := queryForTest(end)
	q.SourceIDs = []string{"source"}
	q.Kind, q.Outcome = "all", "all"
	q.Start, q.End = end.Add(-15*time.Minute), end
	id := func(n int) bson.ObjectID { return mustObjectID(fmt.Sprintf("%024x", n)) }
	at := func(offset time.Duration) time.Time { return end.Add(offset) }
	var docs []map[string]any
	add := func(n int, initiated, created any) {
		doc := map[string]any{
			"_id": id(n), "RequestPollingType": "Synchronous ", "StatusCode": "OK",
			"ControllerName": "Payroll", "ActionName": "Run", "_auditMetadataOversized": false,
		}
		if _, absent := initiated.(pipelineMissing); !absent {
			doc["RequestInitiatedAt"] = initiated
		}
		if created != nil {
			doc["CreatedOn"] = created
		}
		docs = append(docs, doc)
	}
	missing := pipelineMissing{}
	// In the window, distinct and millisecond-grained times.
	add(1, at(-14*time.Minute-59*time.Second), nil)
	add(2, at(-12*time.Minute), nil)
	add(3, at(-12*time.Minute+7*time.Millisecond), nil)
	add(4, at(-9*time.Minute), nil)
	// Three rows tied on one timestamp, ids out of arrival order.
	add(9, at(-6*time.Minute), nil)
	add(5, at(-6*time.Minute), nil)
	add(7, at(-6*time.Minute), nil)
	add(6, at(-3*time.Minute), nil)
	add(8, at(-time.Millisecond), nil)
	// Window edges: the start is inclusive, the end exclusive.
	add(10, at(-15*time.Minute), nil)
	add(11, at(0), nil)
	// Outside the window.
	add(12, at(-15*time.Minute-time.Millisecond), nil)
	add(13, at(time.Hour), nil)
	// No usable start time: the creation time places the row.
	add(14, missing, at(-10*time.Minute))
	add(15, nil, at(-6*time.Minute))
	add(16, nil, at(-4*time.Minute))
	add(17, missing, at(-6*time.Minute+time.Millisecond))
	add(18, nil, at(time.Hour))
	add(19, missing, nil)
	// A start time outside the window wins over a creation time inside it.
	add(20, at(-time.Hour), at(-5*time.Minute))
	// A start time that is not a date never places a row, whatever CreatedOn says.
	add(21, "yesterday", at(-5*time.Minute))
	return keysetFixture{q: q, docs: docs}
}

// windowMatches is an independent statement of the first $match stage: the
// $gte and $lt bounds on RequestInitiatedAt, or the same bounds on CreatedOn for
// a row whose start time is missing or null. Values of another type never match.
func windowMatches(t *testing.T, stage bson.D, doc map[string]any) bool {
	t.Helper()
	if len(stage) == 0 || stage[0].Key != "$or" {
		t.Fatalf("first match stage = %#v, want a leading $or", stage)
	}
	for _, key := range stage[1:] {
		t.Fatalf("unexpected extra condition in the window match: %s", key.Key)
	}
	inRange := func(field string, ops bson.D) bool {
		value, ok := doc[field].(time.Time)
		if !ok {
			return false
		}
		for _, op := range ops {
			bound := op.Value.(time.Time)
			switch op.Key {
			case "$gte":
				if value.Before(bound) {
					return false
				}
			case "$lt":
				if !value.Before(bound) {
					return false
				}
			default:
				t.Fatalf("unsupported window operator %s", op.Key)
			}
		}
		return true
	}
	branches := stage[0].Value.(bson.A)
	startBranch := branches[0].(bson.D)
	if inRange(startBranch[0].Key, startBranch[0].Value.(bson.D)) {
		return true
	}
	fallback := branches[1].(bson.D)[0].Value.(bson.A)
	if _, present := doc["RequestInitiatedAt"]; present && doc["RequestInitiatedAt"] != nil {
		return false
	}
	created := fallback[1].(bson.D)
	return inRange(created[0].Key, created[0].Value.(bson.D))
}

// runKeysetPipeline plays the pipeline against docs: the window match, the
// derived fields, the later matches, the sort and the limit. It returns the
// surviving rows in order and how many documents the window match admitted.
func runKeysetPipeline(t *testing.T, pipeline mongo.Pipeline, docs []map[string]any) ([]map[string]any, int) {
	t.Helper()
	var survivors []map[string]any
	scanned := 0
	for _, doc := range docs {
		if !windowMatches(t, pipeline[0][0].Value.(bson.D), doc) {
			continue
		}
		scanned++
		derived, ok := evalRowSurvives(t, pipeline, doc)
		if ok {
			survivors = append(survivors, derived)
		}
	}
	direction, limit := -1, len(docs)
	for _, stage := range pipeline {
		switch stage[0].Key {
		case "$sort":
			direction = stage[0].Value.(bson.D)[0].Value.(int)
		case "$limit":
			limit = stage[0].Value.(int)
		}
	}
	sort.Slice(survivors, func(i, j int) bool {
		a, b := survivors[i]["_auditAt"].(time.Time), survivors[j]["_auditAt"].(time.Time)
		if !a.Equal(b) {
			return a.Before(b) == (direction == 1)
		}
		ai, bi := survivors[i]["_id"].(bson.ObjectID).Hex(), survivors[j]["_id"].(bson.ObjectID).Hex()
		return (ai < bi) == (direction == 1)
	})
	if len(survivors) > limit {
		survivors = survivors[:limit]
	}
	return survivors, scanned
}

func keysetIDs(rows []map[string]any) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row["_id"].(bson.ObjectID).Hex()[20:]
	}
	return ids
}

// TestKeysetPagesEqualTheFullWindowSortAndScanLess pages the pipeline with and
// without the cursor-narrowed window match. The wide window is the earlier
// behaviour (every page matches the whole query window and leaves the keyset to
// a later stage); the pages must be identical, and later pages must scan fewer
// documents.
func TestKeysetPagesEqualTheFullWindowSortAndScanLess(t *testing.T) {
	fx := keysetFixtureDocs()
	cohort := [5]string{"", "", "", "all", "all"}
	for _, oldest := range []bool{false, true} {
		// Reference: every row that reaches the sort, no cursor, no limit.
		full, _ := runKeysetPipeline(t, requestPipelineOrdered(fx.q, cohort, "", true, oldest, "source", nil, 1000), fx.docs)
		if len(full) != 14 {
			t.Fatalf("oldest=%v: fixture yields %d rows, want 14 (ids %v)", oldest, len(full), keysetIDs(full))
		}
		for _, limit := range []int{1, 2, 3, 4, 7, 14, 15} {
			var after *pageCursor
			var paged []string
			narrowerPages := 0
			for page := 0; page < 40; page++ {
				narrow := requestPipelineOrdered(fx.q, cohort, "", true, oldest, "source", after, limit+1)
				// The earlier pipeline: the same stages behind the whole-window match.
				wide := append(mongo.Pipeline{requestPipelineOrdered(fx.q, cohort, "", true, oldest, "source", nil, limit+1)[0]}, narrow[1:]...)
				gotNarrow, scannedNarrow := runKeysetPipeline(t, narrow, fx.docs)
				gotWide, scannedWide := runKeysetPipeline(t, wide, fx.docs)
				if !reflect.DeepEqual(keysetIDs(gotNarrow), keysetIDs(gotWide)) {
					t.Fatalf("oldest=%v limit=%d page=%d: narrowed %v, whole window %v", oldest, limit, page, keysetIDs(gotNarrow), keysetIDs(gotWide))
				}
				if scannedNarrow > scannedWide {
					t.Fatalf("oldest=%v limit=%d page=%d: narrowed scan read %d documents, whole window %d", oldest, limit, page, scannedNarrow, scannedWide)
				}
				if scannedNarrow < scannedWide {
					narrowerPages++
				}
				rows := gotNarrow
				more := len(rows) > limit
				if more {
					rows = rows[:limit]
				}
				paged = append(paged, keysetIDs(rows)...)
				if !more {
					break
				}
				last := rows[len(rows)-1]
				at := last["_auditAt"].(time.Time)
				after = &pageCursor{At: at, SourceID: "source", ObjectID: last["_id"].(bson.ObjectID).Hex()}
			}
			if want := keysetIDs(full); !reflect.DeepEqual(paged, want) {
				t.Fatalf("oldest=%v limit=%d: paged %v, want the full sort %v", oldest, limit, paged, want)
			}
			if limit < 14 && narrowerPages == 0 {
				t.Fatalf("oldest=%v limit=%d: no continuation page scanned fewer documents than the whole window", oldest, limit)
			}
		}
	}
}

// TestKeysetCursorFromAnotherSourceOrderingStillMatchesWideWindow covers the
// cursor source order: a cursor from a source that sorts before or after this
// one decides ties on the cursor time differently, and the narrowed window must
// keep those ties.
func TestKeysetCursorFromAnotherSourceOrderingStillMatchesWideWindow(t *testing.T) {
	fx := keysetFixtureDocs()
	cohort := [5]string{"", "", "", "all", "all"}
	tied := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC).Add(-6 * time.Minute)
	for _, oldest := range []bool{false, true} {
		for _, cursorSource := range []string{"a", "source", "z"} {
			for _, cursorID := range []int{0, 5, 7, 9, 99} {
				after := &pageCursor{At: tied, SourceID: cursorSource, ObjectID: fmt.Sprintf("%024x", cursorID)}
				narrow := requestPipelineOrdered(fx.q, cohort, "", true, oldest, "source", after, 100)
				wide := append(mongo.Pipeline{requestPipelineOrdered(fx.q, cohort, "", true, oldest, "source", nil, 100)[0]}, narrow[1:]...)
				gotNarrow, _ := runKeysetPipeline(t, narrow, fx.docs)
				gotWide, _ := runKeysetPipeline(t, wide, fx.docs)
				if !reflect.DeepEqual(keysetIDs(gotNarrow), keysetIDs(gotWide)) {
					t.Errorf("oldest=%v cursor=%s/%d: narrowed %v, whole window %v", oldest, cursorSource, cursorID, keysetIDs(gotNarrow), keysetIDs(gotWide))
				}
			}
		}
	}
}

// TestRequestsExportWalkMatchesReferenceOrder drives Store.Requests the way the
// CSV export does (the largest page, cursor after cursor) against a fake whose
// Aggregate plays the real pipeline, and compares every page with an
// independent sort of the fixture rows.
func TestRequestsExportWalkMatchesReferenceOrder(t *testing.T) {
	fx := keysetFixtureDocs()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	fx.q.SourceIDs = []string{source.ID}
	var scans []int
	handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		rows, scanned := runKeysetPipeline(t, pipeline, fx.docs)
		scans = append(scans, scanned)
		out := make([]any, len(rows))
		for i, row := range rows {
			at := row["_auditAt"].(time.Time)
			out[i] = requestDocument{ID: row["_id"].(bson.ObjectID), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &at}
		}
		return &fakeCursor{docs: out}, nil
	}
	// Reference: rows placed in the window, newest first, ties by descending id.
	type refRow struct {
		at time.Time
		id string
	}
	var ref []refRow
	for _, doc := range fx.docs {
		placed, ok := doc["RequestInitiatedAt"].(time.Time)
		if !ok {
			if v, present := doc["RequestInitiatedAt"]; present && v != nil {
				continue
			}
			placed, ok = doc["CreatedOn"].(time.Time)
			if !ok {
				continue
			}
		}
		if placed.Before(fx.q.Start) || !placed.Before(fx.q.End) {
			continue
		}
		ref = append(ref, refRow{placed, doc["_id"].(bson.ObjectID).Hex()[20:]})
	}
	sort.Slice(ref, func(i, j int) bool {
		if !ref[i].at.Equal(ref[j].at) {
			return ref[i].at.After(ref[j].at)
		}
		return ref[i].id > ref[j].id
	})
	var want []string
	for _, r := range ref {
		want = append(want, r.id)
	}
	for _, limit := range []int{1, 2, 4, 14, audit.MaxPageSize} {
		scans = nil
		var got []string
		cursor := ""
		for page := 0; page < 100; page++ {
			result, err := s.Requests(context.Background(), audit.PageInput{Query: fx.q, Limit: limit, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range result.Rows {
				got = append(got, row.ID[20:])
			}
			if result.NextCursor == "" {
				break
			}
			cursor = result.NextCursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limit=%d: export walk %v, want %v", limit, got, want)
		}
		if limit < len(want) && !(scans[len(scans)-1] < scans[0]) {
			t.Fatalf("limit=%d: last page scanned %d documents, first %d (%v)", limit, scans[len(scans)-1], scans[0], scans)
		}
	}
}

func TestKeysetScanWindow(t *testing.T) {
	start := time.Date(2026, 3, 9, 11, 45, 0, 0, time.UTC)
	end := start.Add(15 * time.Minute)
	mid := start.Add(5 * time.Minute)
	cases := []struct {
		name         string
		after        *pageCursor
		oldest       bool
		wantS, wantE time.Time
	}{
		{"first page keeps the window", nil, false, start, end},
		{"first oldest page keeps the window", nil, true, start, end},
		{"newest pulls the end to the cursor millisecond, exclusive", &pageCursor{At: mid}, false, start, mid.Add(time.Millisecond)},
		{"newest rounds a sub-millisecond cursor down then adds one", &pageCursor{At: mid.Add(400 * time.Microsecond)}, false, start, mid.Add(time.Millisecond)},
		{"oldest pulls the start to the cursor, inclusive", &pageCursor{At: mid}, true, mid, end},
		{"newest cursor past the end never widens the window", &pageCursor{At: end.Add(time.Hour)}, false, start, end},
		{"oldest cursor before the start never widens the window", &pageCursor{At: start.Add(-time.Hour)}, true, start, end},
		{"newest cursor before the start leaves an empty window", &pageCursor{At: start.Add(-time.Hour)}, false, start, start.Add(-time.Hour + time.Millisecond)},
	}
	for _, c := range cases {
		gotS, gotE := keysetScanWindow(start, end, c.after, c.oldest)
		if !gotS.Equal(c.wantS) || !gotE.Equal(c.wantE) {
			t.Errorf("%s: window [%s, %s), want [%s, %s)", c.name, gotS, gotE, c.wantS, c.wantE)
		}
	}
}
