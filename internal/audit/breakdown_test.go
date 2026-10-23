package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func breakdownFacts(r RequestRecord) RecordFacts {
	return RecordFacts{ID: r.ID, UserID: r.UserID, UserIP: r.UserIP, UserAgent: r.UserAgent, UIVersion: r.UIVersion, URL: r.URL, Status: r.Status}
}

// The fragment of a row, applied to a query, selects every record of that row
// through the shared record filters, and no record of another value, except the
// documented widening: a user agent is a case-insensitive substring.
func TestBreakdownFilterDrillsIntoItsRow(t *testing.T) {
	uid, other := 7, 77
	rows := []RequestRecord{
		{Status: " OK ", UserID: &uid, UserIP: "10.0.0.1", UserAgent: "UA", UIVersion: "1.0"},
		{Status: "OK", UserID: &other, UserIP: "10.0.0.2", UserAgent: "UA/2", UIVersion: "2.0"},
		{Status: "NotFound", UserID: &uid, UserAgent: "$path", UIVersion: "1.0"},
		{Status: "", UserIP: "10.0.0.1"},
	}
	for _, dim := range []string{BreakdownStatusCode, BreakdownUserID, BreakdownUIVersion, BreakdownUserAgent, BreakdownUserIP} {
		for _, keyRow := range rows {
			key := BreakdownKey(dim, keyRow)
			filter := NewBreakdownFilter(dim, key, 0)
			if key == "" {
				if filter != nil {
					t.Errorf("%s: a missing value has no filter, got %#v", dim, filter)
				}
				continue
			}
			if filter == nil {
				t.Fatalf("%s key %q: no filter", dim, key)
			}
			q := filter.Apply(validQuery())
			if err := ValidateQuery(q); err != nil {
				t.Errorf("%s key %q: the merged query is invalid: %v", dim, key, err)
			}
			for i, r := range rows {
				got := RecordFilterMatches(q, breakdownFacts(r))
				same := BreakdownKey(dim, r) == key
				if same && !got {
					t.Errorf("%s key %q row %d: the filter drops a row of the value", dim, key, i)
				}
				if got && !same && dim != BreakdownUserAgent {
					t.Errorf("%s key %q row %d: the filter selects a row of value %q", dim, key, i, BreakdownKey(dim, r))
				}
			}
		}
	}
	// The substring widening is real: "UA" also selects "UA/2".
	q := NewBreakdownFilter(BreakdownUserAgent, "UA", 0).Apply(validQuery())
	if !RecordFilterMatches(q, breakdownFacts(rows[1])) {
		t.Error("a user agent filter is a substring; it should reach UA/2")
	}
}

func TestBreakdownFilterIsAbsentWhenNoRecordFilterFits(t *testing.T) {
	long := strings.Repeat("x", MaxFilterLength+1)
	for name, tc := range map[string]struct {
		dim, key string
		cap      int
	}{
		"missing status":      {BreakdownStatusCode, "", 0},
		"missing user":        {BreakdownUserID, "", 0},
		"polling type":        {BreakdownPollingType, "Synchronous", 0},
		"blank version":       {BreakdownUIVersion, "   ", 0},
		"user id zero":        {BreakdownUserID, "0", 0},
		"user id too large":   {BreakdownUserID, "2147483648", 0},
		"user id not numeric": {BreakdownUserID, "abc", 0},
		"too long":            {BreakdownUserAgent, long, 0},
		"nul byte":            {BreakdownUserIP, "10.0\x00.1", 0},
		"invalid utf8":        {BreakdownUIVersion, "\xff", 0},
		"may be cut":          {BreakdownUserIP, strings.Repeat("1", 64), 64},
	} {
		if f := NewBreakdownFilter(tc.dim, tc.key, tc.cap); f != nil {
			t.Errorf("%s: want no filter, got %#v", name, f)
		}
	}
	if f := NewBreakdownFilter(BreakdownUserIP, strings.Repeat("1", 63), 64); f == nil {
		t.Error("a value below the cap is not cut and keeps its filter")
	}
}

// The fragment travels as Query JSON: each field must carry the name and meaning
// of the Query field it merges into.
func TestBreakdownFilterJSONIsQueryJSON(t *testing.T) {
	raw, err := json.Marshal(BreakdownFilter{UserIDs: []int{7}, UIVersions: []string{"1.0"}, UserAgentContains: "UA", UserIPs: []string{"10.0.0.1"}, StatusCodes: []string{"OK"}})
	if err != nil {
		t.Fatal(err)
	}
	var q Query
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	if len(q.UserIDs) != 1 || q.UserIDs[0] != 7 || len(q.UIVersions) != 1 || q.UserAgentContains != "UA" || len(q.UserIPs) != 1 || len(q.StatusCodes) != 1 {
		t.Fatalf("the fragment %s does not decode into the matching Query fields: %#v", raw, q)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil || len(keys) != 5 {
		t.Fatalf("the fragment must carry exactly its five filters: %s", raw)
	}
}

func TestBreakdownFilterApplyKeepsOtherFilters(t *testing.T) {
	q := validQuery()
	q.UIVersions = []string{"9"}
	q.Completion = StateIncomplete
	got := NewBreakdownFilter(BreakdownUserID, "7", 0).Apply(q)
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 7 || len(got.UIVersions) != 1 || got.UIVersions[0] != "9" || got.Completion != StateIncomplete {
		t.Fatalf("Apply = %#v", got)
	}
	if len(q.UserIDs) != 0 {
		t.Fatal("Apply must not modify its argument")
	}
}
func TestBreakdownBuilderRanksAndBuildsOther(t *testing.T) {
	b := NewBreakdownBuilder(BreakdownUserID, 2)
	for id, n := range map[int]int64{12: 3, 3: 3, 9: 1, 4: 1} {
		b.AddCounts(strconv.Itoa(id), BreakdownCounts{Count: n})
	}
	b.AddCounts("", BreakdownCounts{Count: 3})
	b.AddBin("3", BinIndex(100), 3, 300, 100, 100)
	out, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Ties break with the missing value first, then numerically (3 before 12).
	if len(out.Rows) != 2 || !out.Rows[0].Missing || out.Rows[0].Value != BreakdownMissingLabel || out.Rows[1].Value != "3" {
		t.Fatalf("rows = %#v", out.Rows)
	}
	if out.Total != 11 || out.Distinct != 5 || out.Other == nil || out.Other.Count != 5 || out.Other.Values != 3 || out.Other.Filter != nil {
		t.Fatalf("total=%d distinct=%d other=%#v", out.Total, out.Distinct, out.Other)
	}
	if out.Rows[1].Stats.Count != 3 || out.Rows[0].Stats.Count != 0 {
		t.Fatalf("stats must come from valid bins only: %#v %#v", out.Rows[1].Stats, out.Rows[0].Stats)
	}
}

func TestPooledKindNeverPoolsAcrossKinds(t *testing.T) {
	for kind, want := range map[string]bool{
		"all": true, KindHTTP: true, "": true,
		"synchronous": false, "asynchronous": false, "hangfire": false, KindHTTPUnknown: false,
	} {
		if got := PooledKind(kind); got != want {
			t.Errorf("PooledKind(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestBreakdownBuilderPooledDropsPercentilesKeepsCounts(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		b := NewBreakdownBuilder(BreakdownUserID, 1)
		b.SetPooled(pooled)
		b.AddCounts("3", BreakdownCounts{Count: 4, Incomplete: 1, Count4xx: 1})
		b.AddCounts("9", BreakdownCounts{Count: 2, Count5xx: 1})
		b.AddBin("3", BinIndex(100), 3, 300, 100, 100)
		b.AddBin("9", BinIndex(200), 1, 200, 200, 200)
		out, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if out.PooledKinds != pooled || out.Total != 6 || len(out.Rows) != 1 || out.Other == nil {
			t.Fatalf("pooled=%v: %#v", pooled, out)
		}
		row := out.Rows[0]
		if row.Count != 4 || row.Incomplete != 1 || row.Count4xx != 1 || out.Other.Count != 2 || out.Other.Count5xx != 1 {
			t.Fatalf("pooled=%v: counts changed: %#v %#v", pooled, row, out.Other)
		}
		for name, stats := range map[string]Stats{"row": row.Stats, "other": out.Other.Stats} {
			if pooled && (stats.Count != 0 || stats.P50 != nil || stats.P95 != nil) {
				t.Errorf("%s carries a pooled percentile: %#v", name, stats)
			}
			if !pooled && (stats.Count == 0 || stats.P50 == nil || stats.P95 == nil) {
				t.Errorf("%s lost its percentiles under a single kind: %#v", name, stats)
			}
		}
	}
}

func TestDemoBreakdownFlagsPooledKinds(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	reader, ok := NewDemo(now).(BreakdownReader)
	if !ok {
		t.Fatal("demo repository must implement BreakdownReader")
	}
	for _, kind := range []string{"all", "http", "synchronous"} {
		q := Query{
			SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-15 * time.Minute), End: now,
			BaselineStart: now.Add(-30 * time.Minute), BaselineEnd: now.Add(-15 * time.Minute),
			Kind: kind, Outcome: "all", Metric: "total", BucketSeconds: 10,
		}
		got, err := reader.Breakdown(context.Background(), BreakdownInput{Query: q, Dim: BreakdownUserAgent, Top: 50})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		pooled := kind != "synchronous"
		if got.PooledKinds != pooled || len(got.Rows) == 0 {
			t.Fatalf("%s: PooledKinds = %v, rows = %d", kind, got.PooledKinds, len(got.Rows))
		}
		for _, row := range got.Rows {
			if pooled && (row.Stats.P50 != nil || row.Stats.P95 != nil) {
				t.Errorf("%s: %q carries a pooled percentile", kind, row.Value)
			}
		}
	}
}

func TestValidateBreakdownRejectsBadDimAndTop(t *testing.T) {
	good := BreakdownInput{Query: validQuery(), Dim: BreakdownUserIP}
	if err := ValidateBreakdown(good); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	for name, in := range map[string]BreakdownInput{
		"dim":      {Query: good.Query, Dim: "controller"},
		"top high": {Query: good.Query, Dim: BreakdownUserIP, Top: MaxBreakdownTop + 1},
		"top low":  {Query: good.Query, Dim: BreakdownUserIP, Top: -1},
	} {
		if err := ValidateBreakdown(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
