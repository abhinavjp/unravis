package store

import (
	"api-audit-explorer/internal/audit"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowestReadsEverySourceOnceAndRanksGlobally(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	a := addFakeSource(t, s, "one")
	b := addFakeSource(t, s, "two")
	calls := 0
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.aggregateFn = func(_ context.Context, db, _ string, p mongo.Pipeline) (mongoCursor, error) {
		calls++
		raw, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: p}}, false, false)
		text := string(raw)
		if !strings.Contains(text, `"durationMs":-1`) || strings.Contains(text, "RequestPayload") {
			t.Fatal(text)
		}
		duration := 100.
		if db == b.Database {
			duration = 900
		}
		return &fakeCursor{docs: []any{requestDocument{ID: bson.NewObjectID(), InitiatedAt: &at, DurationMs: &duration}}}, nil
	}
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{a.ID, b.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	p, e := s.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if calls != 2 || len(p.Rows) != 1 || *p.Rows[0].DurationMs != 900 || !p.Truncated || p.NextCursor == "" {
		t.Fatalf("calls=%d page=%+v", calls, p)
	}
}
func TestLegacySlowestDoesNotReturnNewestInstead(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	src := addFakeSource(t, s, "one")
	p, release, _ := s.leaseSource(src.ID)
	defer release()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	docs := []any{}
	for i, ms := range []int{9000, 100} {
		started := at.Add(time.Duration(i) * time.Second)
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "_auditMetadataOversized", Value: false}, {Key: "RequestInitiatedAt", Value: started}, {Key: "RequestCompletedAt", Value: started.Add(time.Duration(ms) * time.Millisecond)}, {Key: "ApiTime", Value: float64(10000 - ms)}})
		docs = append(docs, bson.Raw(raw))
	}
	h.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	var count, byteCount atomic.Int64
	rows, e := readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 1, &count, &byteCount, false, &slowFilter{})
	if e != nil || len(rows) != 1 || *rows[0].request.DurationMs != 9000 {
		t.Fatalf("wrong ranking %v %v", rows, e)
	}
	count.Store(0)
	byteCount.Store(0)
	q.Metric = "api"
	rows, e = readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 1, &count, &byteCount, false, &slowFilter{})
	if e != nil || *rows[0].request.ApiTimeMs != 9900 {
		t.Fatal("API metric ranking mismatch", e)
	}
}

type slowDoc struct {
	id         bson.ObjectID
	at         time.Time
	total, api *float64
}

func slowNum(v float64) *float64 { return &v }

func slowID(n int) bson.ObjectID {
	id, err := bson.ObjectIDFromHex(fmt.Sprintf("%024x", n))
	if err != nil {
		panic(err)
	}
	return id
}

func slowCompare(a, b any) (int, bool) {
	number := func(v any) (float64, bool) {
		switch n := v.(type) {
		case float64:
			return n, true
		case int:
			return float64(n), true
		}
		return 0, false
	}
	if x, ok := number(a); ok {
		y, ok := number(b)
		if !ok {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	}
	switch x := a.(type) {
	case time.Time:
		y, ok := b.(time.Time)
		if !ok {
			return 0, false
		}
		switch {
		case x.Before(y):
			return -1, true
		case x.After(y):
			return 1, true
		}
		return 0, true
	case bson.ObjectID:
		y, ok := b.(bson.ObjectID)
		if !ok {
			return 0, false
		}
		return bytes.Compare(x[:], y[:]), true
	}
	return 0, false
}

// evalSlowMatch evaluates the $match shapes the slowest pipeline emits after
// its $project. A comparison against a missing, null or other-typed field is
// false, like MongoDB's type bracketing.
func evalSlowMatch(t *testing.T, match bson.D, doc map[string]any) bool {
	for _, e := range match {
		switch e.Key {
		case "$or", "$and":
			anyOK := false
			all := true
			for _, branch := range e.Value.(bson.A) {
				ok := evalSlowMatch(t, branch.(bson.D), doc)
				anyOK, all = anyOK || ok, all && ok
			}
			if (e.Key == "$or" && !anyOK) || (e.Key == "$and" && !all) {
				return false
			}
		default:
			value := doc[e.Key]
			condition, isOps := e.Value.(bson.D)
			if !isOps {
				if c, ok := slowCompare(value, e.Value); !ok || c != 0 {
					return false
				}
				continue
			}
			for _, op := range condition {
				c, ok := slowCompare(value, op.Value)
				if !ok {
					return false
				}
				switch op.Key {
				case "$gte":
					ok = c >= 0
				case "$gt":
					ok = c > 0
				case "$lt":
					ok = c < 0
				case "$lte":
					ok = c <= 0
				default:
					t.Fatalf("unsupported operator %s on %s", op.Key, e.Key)
				}
				if !ok {
					return false
				}
			}
		}
	}
	return true
}

// slowTail runs the stages after the pipeline's $project (the matches, the
// sort and the limit) over already projected documents.
func slowTail(t *testing.T, pipeline mongo.Pipeline, docs []slowDoc) []slowDoc {
	t.Helper()
	project := -1
	for i, stage := range pipeline {
		if stage[0].Key == "$project" {
			project = i
		}
	}
	if project < 0 {
		t.Fatal("pipeline has no $project")
	}
	maps := make([]map[string]any, len(docs))
	for i, d := range docs {
		m := map[string]any{"_id": d.id, "initiatedAt": d.at}
		if d.total != nil {
			m["durationMs"] = *d.total
		}
		if d.api != nil {
			m["apiTimeMs"] = *d.api
		}
		maps[i] = m
	}
	order := make([]int, len(docs))
	for i := range order {
		order[i] = i
	}
	for _, stage := range pipeline[project+1:] {
		switch stage[0].Key {
		case "$match":
			kept := order[:0:0]
			for _, i := range order {
				if evalSlowMatch(t, stage[0].Value.(bson.D), maps[i]) {
					kept = append(kept, i)
				}
			}
			order = kept
		case "$sort":
			keys := stage[0].Value.(bson.D)
			sort.SliceStable(order, func(x, y int) bool {
				for _, k := range keys {
					c, ok := slowCompare(maps[order[x]][k.Key], maps[order[y]][k.Key])
					if !ok || c == 0 {
						continue
					}
					if k.Value.(int) < 0 {
						return c > 0
					}
					return c < 0
				}
				return false
			})
		case "$limit":
			if n := int(stage[0].Value.(int)); len(order) > n {
				order = order[:n]
			}
		default:
			t.Fatalf("unexpected tail stage %s", stage[0].Key)
		}
	}
	out := make([]slowDoc, len(order))
	for i, idx := range order {
		out[i] = docs[idx]
	}
	return out
}

type slowFixture struct {
	store   *Store
	sources []audit.Source
	docs    map[string][]slowDoc
	calls   *atomic.Int32
}

// newSlowFixture builds a store whose sources answer the slowest pipeline from
// docs by evaluating its post-projection stages.
func newSlowFixture(t *testing.T, docs map[string][]slowDoc, names ...string) slowFixture {
	t.Helper()
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	f := slowFixture{store: s, docs: docs, calls: &atomic.Int32{}}
	for _, name := range names {
		f.sources = append(f.sources, addFakeSource(t, s, name))
	}
	h.aggregateFn = func(_ context.Context, _, collection string, p mongo.Pipeline) (mongoCursor, error) {
		f.calls.Add(1)
		out := []any{}
		for _, d := range slowTail(t, p, docs[collection]) {
			at := d.at
			doc := requestDocument{ID: d.id, InitiatedAt: &at, DurationMs: d.total, ApiTimeMs: d.api}
			out = append(out, doc)
		}
		return &fakeCursor{docs: out}, nil
	}
	return f
}

func (f slowFixture) query() audit.Query {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{f.sources[0].ID, f.sources[1].ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	return q
}

func slowTieDocs() map[string][]slowDoc {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	docs := map[string][]slowDoc{"one": nil, "two": nil}
	n := 1
	for _, name := range []string{"one", "two"} {
		for i := 0; i < 9; i++ {
			// Heavy ties: three durations, three start times, shared across sources.
			total := []float64{100, 300, 300, 500, 300, 100, 900, 300, 500}[i]
			start := at.Add(time.Duration(i%3) * time.Second)
			docs[name] = append(docs[name], slowDoc{id: slowID(n), at: start, total: slowNum(total), api: slowNum(total / 2)})
			n++
		}
	}
	// Incomplete requests carry no duration and must never be ranked.
	docs["one"] = append(docs["one"], slowDoc{id: slowID(900), at: at, api: slowNum(9999)})
	return docs
}

func slowKeys(rows []audit.RequestRecord) []string {
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.SourceID + "/" + r.ID
	}
	return keys
}

func slowExpected(f slowFixture, metric string, min float64) []string {
	type ranked struct {
		source, id string
		metric     float64
		at         time.Time
	}
	var all []ranked
	for i, name := range []string{"one", "two"} {
		for _, d := range f.docs[name] {
			v := d.total
			if metric == "api" {
				v = d.api
			}
			if d.total == nil || v == nil || *v < min {
				continue
			}
			all = append(all, ranked{f.sources[i].ID, d.id.Hex(), *v, d.at})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.metric != b.metric {
			return a.metric > b.metric
		}
		if !a.at.Equal(b.at) {
			return a.at.After(b.at)
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return a.id > b.id
	})
	keys := make([]string, len(all))
	for i, r := range all {
		keys[i] = r.source + "/" + r.id
	}
	return keys
}

func walkSlowest(t *testing.T, f slowFixture, q audit.Query, limit int, min *float64) []string {
	t.Helper()
	var keys []string
	cursor := ""
	for pages := 0; pages < 100; pages++ {
		p, err := f.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: limit, Cursor: cursor, MinMs: min})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Rows) > limit {
			t.Fatalf("page of %d exceeds limit %d", len(p.Rows), limit)
		}
		keys = append(keys, slowKeys(p.Rows)...)
		if p.NextCursor == "" {
			if p.Truncated {
				t.Fatal("truncated page without a cursor")
			}
			return keys
		}
		cursor = p.NextCursor
	}
	t.Fatal("cursor never ended")
	return nil
}

func TestSlowestCursorIsStableAcrossPages(t *testing.T) {
	for _, metric := range []string{"total", "api"} {
		f := newSlowFixture(t, slowTieDocs(), "one", "two")
		q := f.query()
		q.Metric = metric
		want := slowExpected(f, metric, 0)
		if len(want) != 18 {
			t.Fatalf("fixture: %d", len(want))
		}
		for _, limit := range []int{1, 4, 5, 18, 50} {
			got := walkSlowest(t, f, q, limit, nil)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s limit %d:\ngot  %v\nwant %v", metric, limit, got, want)
			}
		}
	}
}

func TestSlowestCursorBindsToQueryThresholdAndSource(t *testing.T) {
	f := newSlowFixture(t, slowTieDocs(), "one", "two")
	q := f.query()
	min := 100.0
	p, err := f.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 3, MinMs: &min})
	if err != nil || p.NextCursor == "" {
		t.Fatalf("%v %+v", err, p)
	}
	other := 300.0
	changed := q
	changed.Metric = "api"
	forged := func(mutate func(*pageCursor)) string {
		raw, _ := base64.RawURLEncoding.DecodeString(p.NextCursor)
		var c pageCursor
		_ = json.Unmarshal(raw, &c)
		mutate(&c)
		raw, _ = json.Marshal(c)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	for name, in := range map[string]audit.PageInput{
		"threshold changed": {Query: q, Limit: 3, MinMs: &other, Cursor: p.NextCursor},
		"threshold dropped": {Query: q, Limit: 3, Cursor: p.NextCursor},
		"metric changed":    {Query: changed, Limit: 3, MinMs: &min, Cursor: p.NextCursor},
		"not base64":        {Query: q, Limit: 3, MinMs: &min, Cursor: "!!"},
		"no metric":         {Query: q, Limit: 3, MinMs: &min, Cursor: forged(func(c *pageCursor) { c.Metric = nil })},
		"unselected source": {Query: q, Limit: 3, MinMs: &min, Cursor: forged(func(c *pageCursor) { c.SourceID = "elsewhere" })},
		"bad object id":     {Query: q, Limit: 3, MinMs: &min, Cursor: forged(func(c *pageCursor) { c.ObjectID = "zz" })},
	} {
		if _, err := f.store.Slowest(context.Background(), in); !errors.Is(err, audit.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Row cursors from the newest-first request list never work here.
	if _, err := f.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 3, MinMs: &min, Cursor: forged(func(c *pageCursor) { c.Fingerprint = pageFingerprint(q, "") })}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Errorf("request-list fingerprint accepted: %v", err)
	}
}

func TestSlowestHonoursMinObserved(t *testing.T) {
	for _, metric := range []string{"total", "api"} {
		f := newSlowFixture(t, slowTieDocs(), "one", "two")
		q := f.query()
		q.Metric = metric
		for _, min := range []float64{0, 50, 150, 300, 301, 900, 901, 1e9} {
			m := min
			want := slowExpected(f, metric, min)
			got := walkSlowest(t, f, q, 4, &m)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s min %v:\ngot  %v\nwant %v", metric, min, got, want)
			}
		}
	}
	// The minimum is inclusive: total 300 passes min 300 and 301 drops it.
	f := newSlowFixture(t, slowTieDocs(), "one", "two")
	min := 300.0
	p, err := f.store.Slowest(context.Background(), audit.PageInput{Query: f.query(), Limit: 50, MinMs: &min})
	if err != nil || len(p.Rows) != 14 {
		t.Fatalf("%v %d", err, len(p.Rows))
	}
}

func TestLegacySlowestHonoursMinAndCursor(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	src := addFakeSource(t, s, "one")
	p, release, _ := s.leaseSource(src.ID)
	defer release()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	docs := []any{}
	type spec struct {
		ms, offset int
	}
	specs := []spec{{9000, 0}, {9000, 1}, {9000, 1}, {5000, 0}, {300, 2}, {300, 2}, {100, 3}}
	for i, sp := range specs {
		started := at.Add(time.Duration(sp.offset) * time.Second)
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: slowID(i + 1)}, {Key: "_auditMetadataOversized", Value: false}, {Key: "RequestInitiatedAt", Value: started}, {Key: "RequestCompletedAt", Value: started.Add(time.Duration(sp.ms) * time.Millisecond)}, {Key: "ApiTime", Value: float64(sp.ms) / 2}})
		docs = append(docs, bson.Raw(raw))
	}
	h.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: append([]any(nil), docs...)}, nil
	}
	read := func(after *pageCursor, min *float64, max int) []string {
		var count, byteCount atomic.Int64
		rows, err := readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, after, nil, max, &count, &byteCount, false, &slowFilter{min: min})
		if err != nil {
			t.Fatal(err)
		}
		return slowKeys(rowsToRecords(rows))
	}
	id := func(n int) string { return src.ID + "/" + slowID(n).Hex() }
	// Order: 9000@+1s (ids 3, 2), 9000@+0s (1), 5000, 300@+2s (6, 5), 100.
	all := read(nil, nil, 10)
	want := []string{id(3), id(2), id(1), id(4), id(6), id(5), id(7)}
	if strings.Join(all, ",") != strings.Join(want, ",") {
		t.Fatalf("order\ngot  %v\nwant %v", all, want)
	}
	min := 300.0
	if got := read(nil, &min, 10); strings.Join(got, ",") != strings.Join(want[:6], ",") {
		t.Fatalf("min 300: %v", got)
	}
	min = 301
	if got := read(nil, &min, 10); strings.Join(got, ",") != strings.Join(want[:4], ",") {
		t.Fatalf("min 301: %v", got)
	}
	// Resume after every row: the rest of the ranking, no repeats and no gaps.
	for i := range want {
		rows, err := func() ([]pageRow, error) {
			var count, byteCount atomic.Int64
			return readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 10, &count, &byteCount, false, &slowFilter{})
		}()
		if err != nil {
			t.Fatal(err)
		}
		metric := *audit.RequestMetric(rows[i].request, q.Metric)
		after := &pageCursor{At: rows[i].at, SourceID: src.ID, ObjectID: rows[i].object.Hex(), Metric: &metric}
		if got := read(after, nil, 10); strings.Join(got, ",") != strings.Join(want[i+1:], ",") {
			t.Fatalf("after row %d: %v", i, got)
		}
	}
}

func rowsToRecords(rows []pageRow) []audit.RequestRecord {
	out := make([]audit.RequestRecord, len(rows))
	for i := range rows {
		out[i] = rows[i].request
		out[i].ID = rows[i].object.Hex()
	}
	return out
}
