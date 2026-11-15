package store

import (
	"context"
	"crypto/tls"
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

// simDoc is one stored document: its fields for the match test and the BSON the
// reader decodes.
type simDoc struct {
	id     string
	fields map[string]any
	raw    bson.Raw
}

func newSimDoc(t testing.TB, n int, fields map[string]any) simDoc {
	t.Helper()
	id := bson.NewObjectID()
	doc := bson.D{{Key: "_id", Value: id}, {Key: "_auditMetadataOversized", Value: false}}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		doc = append(doc, bson.E{Key: k, Value: fields[k]})
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"_id": id.Hex()}
	for k, v := range fields {
		all[k] = v
	}
	return simDoc{id: fmt.Sprintf("%012d", n), fields: all, raw: raw}
}

// simServer plays the part of the database for the first $match, $sort by _id and
// $limit of a legacy metadata pipeline. It understands only the plain operators
// MongoDB 3.4 has ($and, $or, $in, $exists, $gt, $gte, $lt, $lte and equality) and
// refuses anything else, so a pipeline that slipped in an operator 3.4 lacks
// fails the test. cap models a server whose scan would run past the reader's
// record cap: it returns at most cap+1 rows, as the reader's sentinel row does.
func simServer(t testing.TB, docs []simDoc, serverCap int) func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
	return func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		if len(pipeline) < 3 || pipeline[0][0].Key != "$match" {
			t.Fatalf("not a legacy metadata pipeline: %v", pipeline)
		}
		match, ok := pipeline[0][0].Value.(bson.D)
		if !ok {
			t.Fatalf("first $match is %T", pipeline[0][0].Value)
		}
		var hits []simDoc
		for _, d := range docs {
			if simMatch(t, d.fields, match) {
				hits = append(hits, d)
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].id < hits[j].id })
		limit := serverCap + 1
		if len(hits) > limit {
			hits = hits[:limit]
		}
		out := make([]any, len(hits))
		for i, d := range hits {
			out[i] = d.raw
		}
		return &fakeCursor{docs: out}, nil
	}
}

func simMatch(t testing.TB, doc map[string]any, cond any) bool {
	t.Helper()
	switch c := cond.(type) {
	case bson.D:
		for _, e := range c {
			if !simClause(t, doc, e.Key, e.Value) {
				return false
			}
		}
		return true
	case bson.M:
		for k, v := range c {
			if !simClause(t, doc, k, v) {
				return false
			}
		}
		return true
	}
	t.Fatalf("unsupported match %T", cond)
	return false
}

func simClause(t testing.TB, doc map[string]any, key string, value any) bool {
	t.Helper()
	switch key {
	case "$and":
		for _, part := range value.(bson.A) {
			if !simMatch(t, doc, part) {
				return false
			}
		}
		return true
	case "$or":
		for _, part := range value.(bson.A) {
			if simMatch(t, doc, part) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(key, "$") {
		t.Fatalf("operator %s is not available on MongoDB 3.4 here", key)
	}
	have, present := doc[key]
	switch v := value.(type) {
	case bson.D:
		if len(v) > 0 && strings.HasPrefix(v[0].Key, "$") {
			for _, op := range v {
				if !simOperator(t, have, present, op.Key, op.Value) {
					return false
				}
			}
			return true
		}
	case bson.M:
		for op, arg := range v {
			if !simOperator(t, have, present, op, arg) {
				return false
			}
		}
		return true
	}
	return simEqual(have, present, value)
}

func simOperator(t testing.TB, have any, present bool, op string, arg any) bool {
	t.Helper()
	switch op {
	case "$in":
		for _, item := range arg.(bson.A) {
			if simEqual(have, present, item) {
				return true
			}
		}
		return false
	case "$exists":
		return present == arg.(bool)
	case "$gt", "$gte", "$lt", "$lte":
		a, aok := have.(time.Time)
		b, bok := arg.(time.Time)
		if !aok || !bok {
			return false
		}
		switch op {
		case "$gt":
			return a.After(b)
		case "$gte":
			return !a.Before(b)
		case "$lt":
			return a.Before(b)
		}
		return !a.After(b)
	}
	t.Fatalf("operator %s is not available on MongoDB 3.4 here", op)
	return false
}

func simEqual(have any, present bool, want any) bool {
	if want == nil {
		return !present || have == nil
	}
	if !present {
		return false
	}
	if a, ok := have.(time.Time); ok {
		b, ok := want.(time.Time)
		return ok && a.Equal(b)
	}
	return fmt.Sprintf("%T:%v", simNumber(have), simNumber(have)) == fmt.Sprintf("%T:%v", simNumber(want), simNumber(want))
}

func simNumber(v any) any {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	}
	return v
}

// activityFixture is a window of `many` rows of user 7 with a few rows of user 42
// spread through it, one overlap row of each user that began before the window,
// and one row of user 42 outside the window altogether.
func activityFixture(t testing.TB, start time.Time, many int) ([]simDoc, []string) {
	t.Helper()
	var docs []simDoc
	n := 0
	add := func(fields map[string]any) string {
		n++
		d := newSimDoc(t, n, fields)
		docs = append(docs, d)
		return d.fields["_id"].(string)
	}
	row := func(user int, at time.Time, done time.Duration) map[string]any {
		return map[string]any{"ControllerName": "Payroll", "ActionName": "Run", "UserId": user, "RequestInitiatedAt": at, "CreatedOn": at, "RequestCompletedAt": at.Add(done), "StatusCode": "200", "RequestPollingType": "Synchronous"}
	}
	var wanted []string
	// The overlap row of user 7 sorts first, then the bulk, then the few rows of
	// user 42, so any scan cap spends itself on user 7 before it meets user 42.
	add(row(7, start.Add(-time.Hour), 90*time.Minute))
	for i := 0; i < many; i++ {
		add(row(7, start.Add(time.Duration(i%50)*time.Second), time.Second))
	}
	wanted = append(wanted, add(row(42, start.Add(5*time.Second), time.Second)))
	wanted = append(wanted, add(row(42, start.Add(9*time.Second), time.Second)))
	wanted = append(wanted, add(row(42, start.Add(-time.Hour), 90*time.Minute)))
	add(row(42, start.Add(24*time.Hour), time.Second))
	return docs, wanted
}

func idsOf(rows []pageRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.request.ID
	}
	sort.Strings(out)
	return out
}

// A user filter that matches a few rows among many returns them even though the
// legacy scan cap is smaller than the number of rows in the window: the filter
// must reach the database, not run after the cap.
func TestLegacyActivityUserFilterRunsBeforeScanCap(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	docs, wanted := activityFixture(t, start, 40)
	sort.Strings(wanted)

	const serverCap = 10 // far fewer than the 40+ rows of the window
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return legacyFakeClient{handle}, nil
	})
	src := addFakeSource(t, s, "one")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	handle.aggregateFn = simServer(t, docs, serverCap)

	user := 42
	q, err := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: start, End: start.Add(time.Minute), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	q.UserIDs = []int{user}
	pipeline := legacyRowMetadataPipeline(activityPipeline(q, src.ID, nil, 101))

	// The reader's record counter is shared, so seeding it models a cap of serverCap.
	var records, bytes atomic.Int64
	records.Store(maxLegacyMetadataRecords - serverCap)
	rows, err := readLegacyRequestRowsMode(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, pipeline, 100, &records, &bytes, true)
	if err != nil {
		t.Fatalf("a filter matching 3 rows failed under a cap of %d: %v", serverCap, err)
	}
	got := idsOf(rows)
	if strings.Join(got, ",") != strings.Join(wanted, ",") {
		t.Fatalf("rows %v, want %v", got, wanted)
	}
	for _, r := range rows {
		if r.request.UserID == nil || *r.request.UserID != user {
			t.Fatalf("row of another user returned: %+v", r.request)
		}
	}
}

// The same through Activity itself, at the real scan cap, so the wiring from the
// store method to the legacy reader is covered too.
func TestLegacyActivityUserFilterAtRealScanCap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds 100k documents")
	}
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	docs, wanted := activityFixture(t, start, int(maxLegacyMetadataRecords)+50)
	sort.Strings(wanted)

	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return legacyFakeClient{handle}, nil
	})
	src := addFakeSource(t, s, "one")
	handle.aggregateFn = simServer(t, docs, int(maxLegacyMetadataRecords))

	user := 42
	input := audit.ActivityInput{SourceIDs: []string{src.ID}, Start: start, End: start.Add(time.Minute), Limit: 100, UserID: &user}
	page, err := s.Activity(context.Background(), input)
	if err != nil {
		t.Fatalf("filtered activity failed at the scan cap: %v", err)
	}
	var got []string
	for _, r := range page.Rows {
		got = append(got, r.ID)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wanted, ",") {
		t.Fatalf("rows %v, want %v", got, wanted)
	}

	// Unfiltered, the same window is over the cap and says so instead of cutting.
	input.UserID = nil
	_, err = s.Activity(context.Background(), input)
	if !errors.Is(err, audit.ErrLegacyScanLimit) || !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("an unfiltered scan over the cap must fail visibly, got %v", err)
	}
}

// The controller and action half of an operation filter is pushed down as well.
func TestLegacyActivityOperationFilterRunsBeforeScanCap(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	docs, _ := activityFixture(t, start, 40)
	odd := newSimDoc(t, 9999, map[string]any{"ControllerName": "Leave", "ActionName": "Approve", "UserId": 7, "RequestInitiatedAt": start.Add(3 * time.Second), "CreatedOn": start.Add(3 * time.Second), "RequestCompletedAt": start.Add(4 * time.Second), "StatusCode": "200", "RequestPollingType": "Synchronous"})
	docs = append(docs, odd)

	const serverCap = 10
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return legacyFakeClient{handle}, nil
	})
	src := addFakeSource(t, s, "one")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	handle.aggregateFn = simServer(t, docs, serverCap)

	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: start, End: start.Add(time.Minute), Limit: 100})
	q.Operation = "Leave.Approve"
	pipeline := legacyRowMetadataPipeline(activityPipeline(q, src.ID, nil, 101))
	var records, bytes atomic.Int64
	records.Store(maxLegacyMetadataRecords - serverCap)
	rows, err := readLegacyRequestRowsMode(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, pipeline, 100, &records, &bytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].request.Controller != "Leave" {
		t.Fatalf("rows %+v, want the one Leave.Approve row", rows)
	}
}

// A filter MongoDB 3.4 cannot express (here the derived completion state) stays in
// Go. When the scan hits the cap the read fails with the legacy scan-limit error
// that names the narrowing to do, never a silently cut list; below the cap the Go
// filter still decides.
func TestLegacyGoOnlyFilterHitsVisibleScanLimit(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	docs, _ := activityFixture(t, start, 40)

	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return legacyFakeClient{handle}, nil
	})
	src := addFakeSource(t, s, "one")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	q := queryForTest(start.Add(time.Minute))
	q.SourceIDs = []string{src.ID}
	q.Start, q.End = start, start.Add(time.Minute)
	q.Kind, q.Outcome = "all", "all"
	q.Completion = audit.StateIncomplete // derived: no 3.4 operator can test it
	cohort := [5]string{"", "", "", "all", "all"}
	pipeline := legacyRowMetadataPipeline(requestPipelineState(q, cohort, "", src.ID, nil, 101))

	// Over the cap: visible failure with the narrowing advice.
	const serverCap = 10
	handle.aggregateFn = simServer(t, docs, serverCap)
	var records, bytes atomic.Int64
	records.Store(maxLegacyMetadataRecords - serverCap)
	_, err = readLegacyRequestRowsState(context.Background(), p, q, cohort, "", nil, pipeline, 100, &records, &bytes, false, nil)
	if !errors.Is(err, audit.ErrLegacyScanLimit) || !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("a Go-side filter over the cap must report the scan limit, got %v", err)
	}
	if !strings.Contains(err.Error(), "narrow") {
		t.Fatalf("the limit error gives no advice: %v", err)
	}

	// Under the cap the Go filter is applied: every row here is complete, so
	// none is incomplete.
	handle.aggregateFn = simServer(t, docs, 1000)
	records.Store(0)
	bytes.Store(0)
	rows, err := readLegacyRequestRowsState(context.Background(), p, q, cohort, "", nil, pipeline, 100, &records, &bytes, false, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%d err=%v, want none and no error", len(rows), err)
	}
}

// The filter stage that follows the overlap match is merged into the legacy
// first $match, and only when every operator in it exists on MongoDB 3.4.
func TestLegacyFirstMatchMergesOnlyPlainFilters(t *testing.T) {
	plain := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "A", Value: 1}}}},
		bson.D{{Key: "$match", Value: bson.D{{Key: "B", Value: bson.D{{Key: "$in", Value: bson.A{1, "1"}}}}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "X", Value: 1}}}},
	}
	first, ok := legacyFirstMatch(plain)
	if !ok || !strings.Contains(activityJSON(t, first), `"$and"`) || !strings.Contains(activityJSON(t, first), `"B"`) {
		t.Fatalf("plain filter not merged: %s", activityJSON(t, first))
	}

	for _, op := range []string{"$expr", "$convert", "$toLong", "$trim", "$toDate", "$where"} {
		unsafe := mongo.Pipeline{
			bson.D{{Key: "$match", Value: bson.D{{Key: "A", Value: 1}}}},
			bson.D{{Key: "$match", Value: bson.D{{Key: op, Value: bson.D{{Key: "$eq", Value: bson.A{"$B", 1}}}}}}},
		}
		first, ok := legacyFirstMatch(unsafe)
		if !ok || strings.Contains(activityJSON(t, first), op) {
			t.Fatalf("%s reached the legacy first $match: %s", op, activityJSON(t, first))
		}
	}

	// A $set directly after the first $match (every non-activity pipeline) leaves
	// the first $match exactly as it was.
	only := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "A", Value: 1}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "X", Value: 1}}}},
		bson.D{{Key: "$match", Value: bson.D{{Key: "_auditState", Value: "valid"}}}},
	}
	first, ok = legacyFirstMatch(only)
	if !ok || activityJSON(t, first) != activityJSON(t, bson.D{{Key: "A", Value: 1}}) {
		t.Fatalf("first $match changed: %s", activityJSON(t, first))
	}
}
