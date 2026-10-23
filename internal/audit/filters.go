package audit

import (
	"strings"
	"unicode/utf8"
)

// maxUserID is the largest user id a record filter accepts: ids are stored as
// 32-bit integers, so a larger number can never match.
const maxUserID = 2147483647

// Record filters are shared by the summary, the request lists, the slowest list
// and the demo repository. The server pipelines apply them to raw fields; the
// MongoDB 3.4 adapter and the demo repository apply them in Go through
// RecordFilterMatches and StateMatches.

// HasRecordFilters reports whether q narrows records beyond kind, outcome,
// controller and action.
func (q Query) HasRecordFilters() bool {
	return len(q.UserIDs) > 0 || len(q.UIVersions) > 0 || q.UserAgentContains != "" || len(q.UserIPs) > 0 ||
		q.URLContains != "" || q.AuditID != "" || len(q.StatusCodes) > 0 ||
		(q.Completion != "" && q.Completion != StateAll) || q.Operation != ""
}

// SplitOperation splits "Controller.Action" on the first dot. Both halves must
// be non-empty; the action may itself contain dots.
func SplitOperation(operation string) (controller, action string, ok bool) {
	controller, action, found := strings.Cut(operation, ".")
	if !found || controller == "" || action == "" {
		return "", "", false
	}
	return controller, action, true
}

// Normalized returns q with Operation folded into Controller and Action, which
// are what every pipeline and reader filters on. The receiver is not modified.
// An invalid Operation is left alone; ValidateQuery rejects it first.
func (q Query) Normalized() Query {
	if q.Operation == "" {
		return q
	}
	if controller, action, ok := SplitOperation(q.Operation); ok {
		q.Controller, q.Action = controller, action
		q.Operation = ""
	}
	return q
}

// RecordFacts are the raw record fields a record filter reads. The Truncated
// flags say a projected value was cut at its storage cap, so a Go recheck must
// not use it to disprove a match the server already made on the full value.
type RecordFacts struct {
	ID                                        string
	UserID                                    *int
	UserIP, UserAgent, UIVersion, URL, Status string

	TruncatedURL, TruncatedAgent, TruncatedIP, TruncatedVersion, TruncatedStatus bool
}

// RecordFilterMatches applies the identity, client, URL and status filters of
// q to one record. Completion and Operation are not read here: completion needs
// the derived state (StateMatches) and Operation is folded into Controller and
// Action by Normalized.
func RecordFilterMatches(q Query, f RecordFacts) bool {
	if len(q.UserIDs) > 0 {
		if f.UserID == nil || !containsInt(q.UserIDs, *f.UserID) {
			return false
		}
	}
	if len(q.UIVersions) > 0 && !f.TruncatedVersion && !containsString(q.UIVersions, f.UIVersion) {
		return false
	}
	if len(q.UserIPs) > 0 && !f.TruncatedIP && !containsString(q.UserIPs, f.UserIP) {
		return false
	}
	if q.UserAgentContains != "" && !f.TruncatedAgent && !containsFold(f.UserAgent, q.UserAgentContains) {
		return false
	}
	if q.URLContains != "" && !f.TruncatedURL && !containsFold(f.URL, q.URLContains) {
		return false
	}
	if q.AuditID != "" && !strings.EqualFold(f.ID, q.AuditID) {
		return false
	}
	if len(q.StatusCodes) > 0 && !f.TruncatedStatus {
		status := strings.TrimSpace(f.Status)
		matched := false
		for _, code := range q.StatusCodes {
			matched = matched || strings.EqualFold(status, strings.TrimSpace(code))
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsInt(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// validateRecordFilters bounds every record filter. Text is limited in bytes so
// the regular expression a server builds from it stays small.
func validateRecordFilters(q Query) error {
	if len(q.UserIDs) > MaxUserIDs {
		return invalidInputf("userIds must contain at most %d ids", MaxUserIDs)
	}
	if len(q.UIVersions) > MaxFilterValues {
		return invalidInputf("uiVersions must contain at most %d values", MaxFilterValues)
	}
	if len(q.UserIPs) > MaxFilterValues {
		return invalidInputf("userIps must contain at most %d values", MaxFilterValues)
	}
	if len(q.StatusCodes) > MaxStatusCodes {
		return invalidInputf("statusCodes must contain at most %d values", MaxStatusCodes)
	}
	for _, id := range q.UserIDs {
		if id < 1 || id > maxUserID {
			return invalidInputf("userIds must be between 1 and %d", maxUserID)
		}
	}
	for name, value := range map[string]string{"userAgentContains": q.UserAgentContains, "urlContains": q.URLContains, "operation": q.Operation, "auditId": q.AuditID} {
		if len(value) > MaxFilterLength {
			return invalidInputf("%s must be at most %d bytes", name, MaxFilterLength)
		}
		if !plainText(value) {
			return invalidInputf("%s must be valid text without NUL characters", name)
		}
	}
	for name, values := range map[string][]string{"uiVersions": q.UIVersions, "userIps": q.UserIPs, "statusCodes": q.StatusCodes} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return invalidInputf("%s must not contain blank values", name)
			}
			if len(value) > MaxFilterLength {
				return invalidInputf("%s values must be at most %d bytes", name, MaxFilterLength)
			}
			if !plainText(value) {
				return invalidInputf("%s values must be valid text without NUL characters", name)
			}
		}
	}
	if q.AuditID != "" && !isObjectIDHex(q.AuditID) {
		return invalidInputf("auditId must be 24 hexadecimal characters")
	}
	switch q.Completion {
	case "", StateAll, StateComplete, StateIncomplete, StatePending, StateInvalid:
	default:
		return invalidInputf("completion must be complete, incomplete, pending, invalid, or all")
	}
	if q.Operation != "" {
		controller, action, ok := SplitOperation(q.Operation)
		if !ok {
			return invalidInputf("operation must be Controller.Action")
		}
		if (q.Controller != "" && q.Controller != controller) || (q.Action != "" && q.Action != action) {
			return invalidInputf("operation conflicts with the controller and action filters")
		}
	}
	return nil
}

// plainText reports whether value is valid UTF-8 without a NUL character, which
// a server pattern or a stored string comparison cannot carry safely.
func plainText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func isObjectIDHex(value string) bool {
	if len(value) != 24 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
