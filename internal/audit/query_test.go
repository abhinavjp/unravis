package audit

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func validQuery() Query {
	currentStart := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return Query{
		SourceIDs: []string{"source-a"},
		Start:     currentStart, End: currentStart.Add(15 * time.Minute),
		BaselineStart: currentStart.Add(-15 * time.Minute), BaselineEnd: currentStart,
		Kind: "synchronous", Outcome: "all", Metric: "total", BucketSeconds: 10,
	}
}

func TestValidateQueryRejectsInvalidSourceSelection(t *testing.T) {
	cases := map[string]func(*Query){
		"none":      func(q *Query) { q.SourceIDs = nil },
		"blank":     func(q *Query) { q.SourceIDs = []string{" "} },
		"duplicate": func(q *Query) { q.SourceIDs = []string{"x", "x"} },
		"too many": func(q *Query) {
			q.SourceIDs = make([]string, MaxSources+1)
			for i := range q.SourceIDs {
				q.SourceIDs[i] = string(rune('a' + i))
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsInvalidIntervals(t *testing.T) {
	cases := map[string]func(*Query){
		"zero current":      func(q *Query) { q.End = q.Start },
		"reversed current":  func(q *Query) { q.End = q.Start.Add(-time.Second) },
		"zero baseline":     func(q *Query) { q.BaselineEnd = q.BaselineStart },
		"overlap":           func(q *Query) { q.BaselineStart = q.Start.Add(-time.Second); q.BaselineEnd = q.Start.Add(time.Second) },
		"current too long":  func(q *Query) { q.End = q.Start.Add(7*24*time.Hour + time.Second) },
		"baseline too long": func(q *Query) { q.BaselineStart = q.BaselineEnd.Add(-7*24*time.Hour - time.Second) },
		"zero time":         func(q *Query) { q.Start = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsInvalidEnumsAndBuckets(t *testing.T) {
	cases := map[string]func(*Query){
		"kind": func(q *Query) { q.Kind = "unknown" }, "empty kind": func(q *Query) { q.Kind = "" },
		"outcome": func(q *Query) { q.Outcome = "maybe" }, "metric": func(q *Query) { q.Metric = "average" },
		"seconds low": func(q *Query) { q.BucketSeconds = 9 }, "seconds high": func(q *Query) { q.BucketSeconds = 86401 },
		"too many buckets": func(q *Query) {
			q.BucketSeconds = 10
			q.Start = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
			q.End = q.Start.Add(1201 * time.Second)
			q.BaselineEnd = q.Start
			q.BaselineStart = q.BaselineEnd.Add(-10 * time.Minute)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsLongControllerAndAction(t *testing.T) {
	for _, field := range []string{"controller", "action"} {
		t.Run(field, func(t *testing.T) {
			q := validQuery()
			if field == "controller" {
				q.Controller = string(make([]byte, 201))
				for i := range q.Controller {
					q.Controller = q.Controller[:i] + "x" + q.Controller[i+1:]
				}
			} else {
				q.Action = string(make([]byte, 201))
				for i := range q.Action {
					q.Action = q.Action[:i] + "x" + q.Action[i+1:]
				}
			}
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryAllowsAdjacentWindowsAndUTCOffsets(t *testing.T) {
	q := validQuery()
	loc := time.FixedZone("UTC+4", 4*60*60)
	q.Start = time.Date(2026, 10, 22, 16, 0, 0, 0, loc)
	q.End = q.Start.Add(15 * time.Minute)
	q.BaselineEnd = q.Start
	q.BaselineStart = q.BaselineEnd.Add(-15 * time.Minute)
	before := q
	if err := ValidateQuery(q); err != nil {
		t.Fatalf("adjacent windows should be legal: %v", err)
	}
	if !q.Start.Equal(before.Start) || q.Start.Location() != before.Start.Location() {
		t.Fatal("ValidateQuery mutated input")
	}
}

func TestValidateQueryCountsEpochBucketsIntersectingHalfOpenWindows(t *testing.T) {
	t.Run("rejects 121 current buckets from unaligned endpoints", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 0, 5, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected 121 intersecting buckets to be rejected")
		}
	})
	t.Run("rejects 121 baseline buckets", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 20, 0, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineStart = time.Date(2026, 10, 22, 11, 39, 59, 0, time.UTC)
		q.BaselineEnd = time.Date(2026, 10, 22, 11, 59, 59, 0, time.UTC)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected 121 intersecting baseline buckets to be rejected")
		}
	})
	t.Run("rejects a subsecond endpoint that intersects bucket 121", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 0, 0, 1, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected subsecond endpoint bucket count to exceed 120")
		}
	})
	t.Run("accepts exactly 120 aligned buckets", func(t *testing.T) {
		q := validQuery()
		q.BucketSeconds = 10
		q.Start = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err != nil {
			t.Fatalf("aligned 120-bucket windows should be accepted: %v", err)
		}
	})
	t.Run("accepts arbitrary RFC3339 years without UnixNano arithmetic", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(9999, 10, 22, 12, 0, 0, 0, time.UTC)
		q.End = q.Start.Add(time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-time.Minute)
		if err := ValidateQuery(q); err != nil {
			t.Fatalf("short intervals in year 9999 should be accepted: %v", err)
		}
	})
}

func TestValidateQueryRejectsNonFiniteTimesNotRelevant(t *testing.T) {
	_ = math.NaN() // Keeps this test file's standard-library edge-case imports available for histogram tests.
}

func TestValidateQueryRejectsBadAuditID(t *testing.T) {
	for _, id := range []string{"abc", "zzzzzzzzzzzzzzzzzzzzzzzz", "0123456789abcdef0123456789abcdef", "0123456789abcdef0123456 "} {
		q := validQuery()
		q.AuditID = id
		if err := ValidateQuery(q); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("audit id %q accepted: %v", id, err)
		}
	}
	q := validQuery()
	q.AuditID = "0123456789ABCDEF01234567"
	if err := ValidateQuery(q); err != nil {
		t.Fatalf("a 24 character hex id is valid: %v", err)
	}
}

func TestValidateQueryBoundsRecordFilters(t *testing.T) {
	long := strings.Repeat("x", MaxFilterLength+1)
	codes := make([]string, MaxStatusCodes+1)
	for i := range codes {
		codes[i] = "Code" + strings.Repeat("a", i)
	}
	versions := make([]string, MaxFilterValues+1)
	ids := make([]int, MaxUserIDs+1)
	for i := range versions {
		versions[i] = "v" + strings.Repeat("1", i+1)
	}
	for i := range ids {
		ids[i] = i
	}
	cases := map[string]func(*Query){
		"201 byte url filter":       func(q *Query) { q.URLContains = long },
		"201 byte agent filter":     func(q *Query) { q.UserAgentContains = long },
		"201 byte version":          func(q *Query) { q.UIVersions = []string{long} },
		"201 byte ip":               func(q *Query) { q.UserIPs = []string{long} },
		"201 byte status":           func(q *Query) { q.StatusCodes = []string{long} },
		"201 byte operation":        func(q *Query) { q.Operation = "A." + long },
		"21 status codes":           func(q *Query) { q.StatusCodes = codes },
		"21 versions":               func(q *Query) { q.UIVersions = versions },
		"21 ips":                    func(q *Query) { q.UserIPs = versions },
		"51 user ids":               func(q *Query) { q.UserIDs = ids },
		"blank status":              func(q *Query) { q.StatusCodes = []string{" "} },
		"unknown completion":        func(q *Query) { q.Completion = "maybe" },
		"operation without a dot":   func(q *Query) { q.Operation = "PayrollRun" },
		"operation without action":  func(q *Query) { q.Operation = "Payroll." },
		"operation without control": func(q *Query) { q.Operation = ".Run" },
		"operation disagrees":       func(q *Query) { q.Operation = "Payroll.Run"; q.Controller = "Other" },
		"operation action differs":  func(q *Query) { q.Operation = "Payroll.Run"; q.Action = "Other" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
	ok := validQuery()
	ok.StatusCodes = codes[:MaxStatusCodes]
	ok.UIVersions, ok.UserIPs = versions[:MaxFilterValues], versions[:MaxFilterValues]
	ok.UserIDs = ids[:MaxUserIDs]
	ok.URLContains, ok.UserAgentContains = strings.Repeat("x", MaxFilterLength), strings.Repeat("y", MaxFilterLength)
	ok.Completion = "incomplete"
	ok.Operation, ok.Controller, ok.Action = "Payroll.Run.Now", "Payroll", "Run.Now"
	if err := ValidateQuery(ok); err != nil {
		t.Fatalf("filters at their limits are valid: %v", err)
	}
}

func TestNormalizedSplitsOperationOnTheFirstDot(t *testing.T) {
	q := validQuery()
	q.Operation = "Payroll.Run.Now"
	n := q.Normalized()
	if n.Controller != "Payroll" || n.Action != "Run.Now" || n.Operation != "" {
		t.Fatalf("normalized to %q %q %q", n.Controller, n.Action, n.Operation)
	}
	if q.Operation != "Payroll.Run.Now" || q.Controller != "" {
		t.Fatal("Normalized must not modify its receiver")
	}
	plain := validQuery()
	if got := plain.Normalized(); got.Controller != "" || got.Action != "" {
		t.Fatal("a query without an operation is unchanged")
	}
}

func TestRecordFilterMatchesEveryFilter(t *testing.T) {
	seven := 7
	facts := RecordFacts{ID: "0123456789abcdef01234567", UserID: &seven, UserIP: "10.0.0.1", UserAgent: "Mozilla/5.0 Chrome", UIVersion: "2.1", URL: "/api/Payroll/Run?x=1", Status: "NotFound"}
	hit := map[string]Query{
		"none":           {},
		"user id":        {UserIDs: []int{3, 7}},
		"ui version":     {UIVersions: []string{"2.0", "2.1"}},
		"agent ci":       {UserAgentContains: "chrome"},
		"ip":             {UserIPs: []string{"10.0.0.1"}},
		"url ci":         {URLContains: "PAYROLL/run"},
		"audit id":       {AuditID: "0123456789ABCDEF01234567"},
		"status ci":      {StatusCodes: []string{"notfound"}},
		"status numeric": {StatusCodes: []string{"404", "NotFound"}},
		"all together":   {UserIDs: []int{7}, UIVersions: []string{"2.1"}, UserIPs: []string{"10.0.0.1"}, URLContains: "run", StatusCodes: []string{"NOTFOUND"}},
	}
	for name, q := range hit {
		if !RecordFilterMatches(q, facts) {
			t.Errorf("%s should match", name)
		}
	}
	miss := map[string]Query{
		"user id":    {UserIDs: []int{8}},
		"ui version": {UIVersions: []string{"2.10"}},
		"agent":      {UserAgentContains: "firefox"},
		"ip":         {UserIPs: []string{"10.0.0.2"}},
		"url":        {URLContains: "a.b"},
		"audit id":   {AuditID: "ffffffffffffffffffffffff"},
		"status":     {StatusCodes: []string{"OK"}},
		"one of all": {UserIDs: []int{7}, UIVersions: []string{"9.9"}},
	}
	for name, q := range miss {
		if RecordFilterMatches(q, facts) {
			t.Errorf("%s should not match", name)
		}
	}
	if RecordFilterMatches(Query{UserIDs: []int{7}}, RecordFacts{}) {
		t.Error("a row without a user id cannot match a user id filter")
	}
	truncated := RecordFacts{URL: "/short", TruncatedURL: true}
	if !RecordFilterMatches(Query{URLContains: "beyond-the-cap"}, truncated) {
		t.Error("a truncated value cannot disprove a contains filter the server already applied")
	}
}
