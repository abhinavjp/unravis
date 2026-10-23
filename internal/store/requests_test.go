package store

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
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
