package store

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func perfRaw(t *testing.T, doc bson.D) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func filterEntry(name any, ms any) bson.D {
	return bson.D{{Key: "name", Value: name}, {Key: "ms", Value: ms}}
}

func TestRecordPerfProjectionShape(t *testing.T) {
	projection := recordPerfProjection()
	if len(projection) != 14 {
		t.Fatalf("want 14 perf keys, got %d", len(projection))
	}
	allowed := map[string]bool{"$cond": true, "$or": true, "$eq": true, "$type": true, "$map": true, "$slice": true, "$substrCP": true}
	innerKeys := map[string]bool{"input": true, "as": true, "in": true, "name": true, "ms": true, "count": true, "value": true}
	wired := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case bson.D:
			for _, e := range x {
				if strings.HasPrefix(e.Key, "$") {
					if !allowed[e.Key] {
						t.Fatalf("operator %s is not allowed", e.Key)
					}
				} else if !innerKeys[e.Key] {
					t.Fatalf("unexpected inner key %q", e.Key)
				}
				walk(e.Value)
			}
		case bson.A:
			for _, e := range x {
				walk(e)
			}
		case string:
			switch {
			case strings.HasPrefix(x, "$$e."):
				lower := strings.ToLower(x)
				for _, bad := range []string{"text", "command", "sql", "query", "statement", "url"} {
					if strings.Contains(lower, bad) {
						t.Fatalf("element path %q projects a text-like field", x)
					}
				}
			case strings.HasPrefix(x, "$"):
				wired[strings.TrimPrefix(x, "$")] = true
			}
		}
	}
	values := map[string]any{}
	for _, e := range projection {
		if !strings.HasPrefix(e.Key, "_perf") {
			t.Fatalf("key %q must start with _perf", e.Key)
		}
		values[e.Key] = e.Value
		walk(e.Value)
	}
	verified := []string{
		"PreActionFilterTotalMilliseconds", "PreActionFilterTimings",
		"RequestHitCount", "SqlCommandCount", "SqlMilliseconds", "RedisCallCount", "RedisMilliseconds", "HttpCallCount", "HttpMilliseconds",
	}
	if len(wired) != len(verified) {
		t.Fatalf("only the verified fields may be read, got %v", wired)
	}
	for _, path := range verified {
		if !wired[path] {
			t.Fatalf("verified field %s is not read, got %v", path, wired)
		}
	}
	for _, key := range []string{"_perfSlowSql", "_perfSlowHttp", "_perfSteps", "_perfPressure", "_perfTextStored"} {
		if values[key] != nil {
			t.Fatalf("%s is not wired and must project null, got %v", key, values[key])
		}
	}
	for _, key := range []string{"_perfFilters", "_perfFilterTotalMs", "_perfHitCount", "_perfSqlCount", "_perfSqlMs", "_perfRedisCount", "_perfRedisMs", "_perfHttpCount", "_perfHttpMs"} {
		if values[key] == nil {
			t.Fatalf("verified role %s must be projected", key)
		}
	}
	// The filter list is sliced to its cap before it is mapped.
	filters := values["_perfFilters"].(bson.D)[0].Value.(bson.A)[1].(bson.D)[0].Value.(bson.D)
	slice := filters[0].Value.(bson.D)[0].Value.(bson.A)
	if slice[1] != audit.PerfMaxFilters {
		t.Fatalf("filters must be sliced to %d, got %v", audit.PerfMaxFilters, slice[1])
	}
}

func TestDecodeRecordPerfOldShapeIsNil(t *testing.T) {
	if got := decodeRecordPerf(perfRaw(t, bson.D{{Key: "_id", Value: bson.NewObjectID()}})); got != nil {
		t.Fatalf("old-shape document must give nil, got %#v", got)
	}
	nulls := bson.D{}
	for _, f := range recordPerfFields {
		nulls = append(nulls, bson.E{Key: f.key, Value: nil})
	}
	if got := decodeRecordPerf(perfRaw(t, nulls)); got != nil {
		t.Fatalf("all-null document must give nil, got %#v", got)
	}
}

// TestRecordPerfLiveNumberRows covers each number role wired from the live
// probe: the projection reads the stored path through the number guard, and the
// decoder keeps a number, drops a string and leaves a missing key nil.
func TestRecordPerfLiveNumberRows(t *testing.T) {
	rows := []struct {
		key    string
		path   string
		number any
		want   float64
		get    func(*audit.RecordPerf) *float64
	}{
		{"_perfHitCount", "RequestHitCount", int32(3), 3, func(p *audit.RecordPerf) *float64 { return p.HitCount }},
		{"_perfSqlCount", "SqlCommandCount", int32(6), 6, func(p *audit.RecordPerf) *float64 { return p.SQLCount }},
		{"_perfSqlMs", "SqlMilliseconds", 12.25, 12.25, func(p *audit.RecordPerf) *float64 { return p.SQLMs }},
		{"_perfRedisCount", "RedisCallCount", int32(4), 4, func(p *audit.RecordPerf) *float64 { return p.RedisCount }},
		{"_perfRedisMs", "RedisMilliseconds", 1.75, 1.75, func(p *audit.RecordPerf) *float64 { return p.RedisMs }},
		{"_perfHttpCount", "HttpCallCount", int32(2), 2, func(p *audit.RecordPerf) *float64 { return p.HTTPCount }},
		{"_perfHttpMs", "HttpMilliseconds", 33.5, 33.5, func(p *audit.RecordPerf) *float64 { return p.HTTPMs }},
	}
	projection := map[string]any{}
	for _, e := range recordPerfProjection() {
		projection[e.Key] = e.Value
	}
	for _, row := range rows {
		t.Run(row.key, func(t *testing.T) {
			var field *perfField
			for i := range recordPerfFields {
				if recordPerfFields[i].key == row.key {
					field = &recordPerfFields[i]
				}
			}
			if field == nil || field.mongoPath != row.path || field.kind != perfNumber {
				t.Fatalf("row %s must read number path %s, got %#v", row.key, row.path, field)
			}
			want, err := bson.Marshal(bson.D{{Key: "v", Value: perfNumberExpr("$" + row.path)}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := bson.Marshal(bson.D{{Key: "v", Value: projection[row.key]}})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("%s projection is not the guarded number of $%s", row.key, row.path)
			}

			// Another usable value keeps perf non-nil so the row itself is checked.
			anchor := bson.E{Key: "_perfFilterTotalMs", Value: 1.0}
			number := decodeRecordPerf(perfRaw(t, bson.D{anchor, {Key: row.key, Value: row.number}}))
			if number == nil || row.get(number) == nil || *row.get(number) != row.want {
				t.Fatalf("%s as a number must decode to %v, got %#v", row.key, row.want, number)
			}
			text := decodeRecordPerf(perfRaw(t, bson.D{anchor, {Key: row.key, Value: "7"}}))
			if text == nil || row.get(text) != nil {
				t.Fatalf("%s as a string must decode to nil, got %#v", row.key, text)
			}
			missing := decodeRecordPerf(perfRaw(t, bson.D{anchor}))
			if missing == nil || row.get(missing) != nil {
				t.Fatalf("%s missing must decode to nil, got %#v", row.key, missing)
			}
		})
	}
}

func TestDecodeRecordPerfOnlyFilterFields(t *testing.T) {
	raw := perfRaw(t, bson.D{
		{Key: "_perfFilterTotalMs", Value: 42.5},
		{Key: "_perfFilters", Value: bson.A{filterEntry("AuthFilter", 30.0), filterEntry("PermFilter", int32(12))}},
		{Key: "_perfHitCount", Value: nil},
		{Key: "_perfSqlMs", Value: nil},
	})
	got := decodeRecordPerf(raw)
	if got == nil || got.FilterTotalMs == nil || *got.FilterTotalMs != 42.5 || len(got.Filters) != 2 {
		t.Fatalf("filters-only perf wrong: %#v", got)
	}
	if got.Filters[1].Name != "PermFilter" || got.Filters[1].Ms == nil || *got.Filters[1].Ms != 12 {
		t.Fatalf("filter entries wrong: %#v", got.Filters)
	}
	if got.HitCount != nil || got.SQLCount != nil || got.SQLMs != nil || got.RedisCount != nil || got.RedisMs != nil ||
		got.HTTPCount != nil || got.HTTPMs != nil || got.TextStored != nil || got.Steps != nil || got.SlowSQL != nil ||
		got.SlowHTTP != nil || got.Pressure != nil {
		t.Fatalf("everything else must be nil: %#v", got)
	}
}

func TestDecodeRecordPerfNewShapeGivesAllValues(t *testing.T) {
	raw := perfRaw(t, bson.D{
		{Key: "_perfHitCount", Value: int64(9)}, {Key: "_perfFilterTotalMs", Value: 1.5},
		{Key: "_perfSqlCount", Value: int32(4)}, {Key: "_perfSqlMs", Value: 20.0},
		{Key: "_perfRedisCount", Value: int32(2)}, {Key: "_perfRedisMs", Value: 3.0},
		{Key: "_perfHttpCount", Value: int32(1)}, {Key: "_perfHttpMs", Value: 7.0},
		{Key: "_perfFilters", Value: bson.A{filterEntry("F", 1.0)}},
		{Key: "_perfSteps", Value: bson.A{filterEntry("S", 2.0)}},
		{Key: "_perfSlowSql", Value: bson.A{bson.D{{Key: "count", Value: 2.0}, {Key: "ms", Value: 9.0}}}},
		{Key: "_perfSlowHttp", Value: bson.A{bson.D{{Key: "count", Value: 1.0}, {Key: "ms", Value: 5.0}}}},
		{Key: "_perfPressure", Value: bson.A{bson.D{{Key: "name", Value: "Threads"}, {Key: "value", Value: 11.0}}}},
		{Key: "_perfTextStored", Value: true},
	})
	got := decodeRecordPerf(raw)
	if got == nil || got.HitCount == nil || *got.HitCount != 9 || got.SQLCount == nil || *got.SQLCount != 4 ||
		got.RedisMs == nil || *got.RedisMs != 3 || got.HTTPMs == nil || *got.HTTPMs != 7 ||
		len(got.Filters) != 1 || len(got.Steps) != 1 || len(got.SlowSQL) != 1 || *got.SlowSQL[0].Ms != 9 ||
		len(got.SlowHTTP) != 1 || len(got.Pressure) != 1 || got.Pressure[0].Name != "Threads" ||
		got.TextStored == nil || !*got.TextStored {
		t.Fatalf("new-shape perf wrong: %#v", got)
	}
}

func TestDecodeRecordPerfStringNumberGivesNil(t *testing.T) {
	raw := perfRaw(t, bson.D{{Key: "_perfSqlMs", Value: "12"}, {Key: "_perfFilterTotalMs", Value: 3.0}})
	got := decodeRecordPerf(raw)
	if got == nil || got.SQLMs != nil || got.FilterTotalMs == nil {
		t.Fatalf("string sqlMs must be nil: %#v", got)
	}
	if only := decodeRecordPerf(perfRaw(t, bson.D{{Key: "_perfSqlMs", Value: "12"}})); only != nil {
		t.Fatalf("a lone string number leaves nothing usable, want nil, got %#v", only)
	}
}

func TestDecodeRecordPerfCapsLists(t *testing.T) {
	slow := bson.A{}
	filters := bson.A{}
	for i := 0; i < 5; i++ {
		slow = append(slow, bson.D{{Key: "count", Value: float64(i)}, {Key: "ms", Value: float64(i)}})
	}
	for i := 0; i < 30; i++ {
		filters = append(filters, filterEntry("F", 1.0))
	}
	got := decodeRecordPerf(perfRaw(t, bson.D{{Key: "_perfSlowSql", Value: slow}, {Key: "_perfFilters", Value: filters}}))
	if got == nil || len(got.SlowSQL) != audit.PerfMaxSlow || len(got.Filters) != audit.PerfMaxFilters {
		t.Fatalf("lists must be capped: %#v", got)
	}
}

func TestDecodeRecordPerfCutsLongNamesAndDropsEmpty(t *testing.T) {
	long := strings.Repeat("é", 300)
	got := decodeRecordPerf(perfRaw(t, bson.D{{Key: "_perfFilters", Value: bson.A{
		filterEntry(long, 1.0), filterEntry("   ", 2.0), filterEntry(nil, 3.0), filterEntry("Ok", 4.0),
	}}}))
	if got == nil || len(got.Filters) != 2 {
		t.Fatalf("empty names must be dropped: %#v", got)
	}
	if n := len([]rune(got.Filters[0].Name)); n != audit.PerfMaxName {
		t.Fatalf("name has %d runes, want %d", n, audit.PerfMaxName)
	}
}

func TestDecodeRecordPerfDropsNaNInfNegative(t *testing.T) {
	got := decodeRecordPerf(perfRaw(t, bson.D{
		{Key: "_perfHitCount", Value: math.NaN()}, {Key: "_perfSqlMs", Value: -1.0},
		{Key: "_perfHttpMs", Value: math.Inf(1)}, {Key: "_perfRedisMs", Value: 5.0},
	}))
	if got == nil || got.HitCount != nil || got.SQLMs != nil || got.HTTPMs != nil || got.RedisMs == nil {
		t.Fatalf("NaN, Inf and negatives must be nil: %#v", got)
	}
	if none := decodeRecordPerf(perfRaw(t, bson.D{{Key: "_perfHitCount", Value: math.NaN()}, {Key: "_perfSqlMs", Value: -1.0}})); none != nil {
		t.Fatalf("nothing usable must give nil: %#v", none)
	}
}

func TestRecordDetailPerfOldRow(t *testing.T) {
	s, source, handle := recordStore(t, false)
	id := bson.NewObjectID()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{recordDocument(id)}}, nil
	}
	detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Perf != nil {
		t.Fatalf("old row must have nil perf, got %#v", detail.Perf)
	}
}

func TestRecordDetailPerfFromProjectedKeys(t *testing.T) {
	s, source, handle := recordStore(t, false)
	id := bson.NewObjectID()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{recordDocument(id,
			bson.E{Key: "_perfFilterTotalMs", Value: 80.0},
			bson.E{Key: "_perfFilters", Value: bson.A{filterEntry("PermFilter", 70.0)}},
		)}}, nil
	}
	detail, err := s.Record(context.Background(), audit.RecordInput{SourceID: source.ID, ID: id.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Perf == nil || detail.Perf.FilterTotalMs == nil || *detail.Perf.FilterTotalMs != 80 || len(detail.Perf.Filters) != 1 {
		t.Fatalf("perf not carried: %#v", detail.Perf)
	}
}
