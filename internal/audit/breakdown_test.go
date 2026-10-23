package audit

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestBreakdownFilterSelectsExactlyItsKey(t *testing.T) {
	uid := 7
	rows := []RequestRecord{
		{Status: " OK ", UserID: &uid, UserIP: "10.0.0.1", UserAgent: "UA", UIVersion: "1.0", PollingTypeRaw: "Synchronous "},
		{Status: "OK", UserIP: "10.0.0.2", UIVersion: "2.0", PollingTypeRaw: "   "},
		{Status: "", UserID: &uid, UserAgent: "$path", PollingTypeRaw: "Asynchronous"},
	}
	for _, dim := range []string{BreakdownStatusCode, BreakdownUserID, BreakdownUIVersion, BreakdownUserAgent, BreakdownUserIP, BreakdownPollingType} {
		for _, key := range rows {
			want := BreakdownKey(dim, key)
			filter := BreakdownFilter(dim, want)
			for i, r := range rows {
				if got := filter.Matches(r); got != (BreakdownKey(dim, r) == want) {
					t.Errorf("%s key %q row %d: filter match = %v, but its key is %q", dim, want, i, got, BreakdownKey(dim, r))
				}
			}
		}
	}
}

func TestRecordFilterAndsFieldsAndOrsValues(t *testing.T) {
	uid := 3
	r := RequestRecord{UserID: &uid, UserAgent: "A", UIVersion: "1"}
	for name, tc := range map[string]struct {
		f    RecordFilter
		want bool
	}{
		"empty selects all":      {RecordFilter{}, true},
		"or inside a list":       {RecordFilter{UserAgents: []string{"B", "A"}}, true},
		"and across lists":       {RecordFilter{UserAgents: []string{"A"}, UIVersions: []string{"2"}}, false},
		"missing user is not 3":  {RecordFilter{UserIDMissing: true}, false},
		"missing or 3":           {RecordFilter{UserIDMissing: true, UserIDs: []int{3}}, true},
		"empty text means blank": {RecordFilter{UserIPs: []string{""}}, true},
		"blank is not wildcard":  {RecordFilter{UserAgents: []string{""}}, false},
	} {
		if got := tc.f.Matches(r); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", name, got, tc.want)
		}
	}
}

func TestRecordFilterValidateBoundsLists(t *testing.T) {
	many := make([]string, MaxRecordFilterValues+1)
	if err := (RecordFilter{UserAgents: many}).Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("51 values: %v", err)
	}
	if err := (RecordFilter{UserIDs: make([]int, MaxRecordFilterValues+1)}).Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("51 user ids: %v", err)
	}
	if err := (RecordFilter{UserIPs: []string{strings.Repeat("x", 65)}}).Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("65-character ip: %v", err)
	}
	if err := (RecordFilter{UserIPs: []string{strings.Repeat("x", 64)}, UserAgents: []string{"ok"}}).Validate(); err != nil {
		t.Errorf("valid filter: %v", err)
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
