package audit

import (
	"errors"
	"math"
	"testing"
)

func TestValidateQueryRejectsUnsafeRecordFilterValues(t *testing.T) {
	cases := map[string]func(*Query){
		"user id zero":             func(q *Query) { q.UserIDs = []int{0} },
		"user id negative":         func(q *Query) { q.UserIDs = []int{5, -1} },
		"user id above int32":      func(q *Query) { q.UserIDs = []int{math.MaxInt32 + 1} },
		"blank ui version":         func(q *Query) { q.UIVersions = []string{"2.1", " "} },
		"empty ui version":         func(q *Query) { q.UIVersions = []string{""} },
		"blank ip":                 func(q *Query) { q.UserIPs = []string{"\t"} },
		"empty ip":                 func(q *Query) { q.UserIPs = []string{"10.0.0.1", ""} },
		"nul in agent":             func(q *Query) { q.UserAgentContains = "Chr\x00me" },
		"nul in url":               func(q *Query) { q.URLContains = "/a\x00b" },
		"nul in audit id":          func(q *Query) { q.AuditID = "0123456789abcdef0123456\x00" },
		"nul in version":           func(q *Query) { q.UIVersions = []string{"2.\x001"} },
		"nul in ip":                func(q *Query) { q.UserIPs = []string{"10.0.0.\x001"} },
		"nul in status":            func(q *Query) { q.StatusCodes = []string{"Not\x00Found"} },
		"nul in operation":         func(q *Query) { q.Operation = "Payroll.Ru\x00n" },
		"invalid utf8 in agent":    func(q *Query) { q.UserAgentContains = "Chr\xffme" },
		"invalid utf8 in url":      func(q *Query) { q.URLContains = "/a\xc3(" },
		"invalid utf8 in version":  func(q *Query) { q.UIVersions = []string{"2.\xff"} },
		"invalid utf8 in ip":       func(q *Query) { q.UserIPs = []string{"\xfe"} },
		"invalid utf8 in status":   func(q *Query) { q.StatusCodes = []string{"OK\xff"} },
		"invalid utf8 in operation": func(q *Query) { q.Operation = "Payroll.\xffRun" },
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
	ok.UserIDs = []int{1, math.MaxInt32}
	ok.UIVersions, ok.UserIPs = []string{"2.1"}, []string{"10.0.0.1"}
	ok.UserAgentContains, ok.URLContains = "Chrome é", "/api/ü"
	ok.StatusCodes = []string{"NotFound", "404"}
	if err := ValidateQuery(ok); err != nil {
		t.Fatalf("ids 1 to 2147483647 and ordinary text are valid: %v", err)
	}
}
