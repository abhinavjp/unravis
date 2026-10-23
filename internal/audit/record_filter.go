package audit

import (
	"strings"
	"unicode/utf8"
)

// MaxRecordFilterValues bounds each list in a RecordFilter.
const MaxRecordFilterValues = 50

// RecordFilter narrows request rows by audit-evidence fields. It is the drill
// filter a breakdown row carries: every list holds exact values (an empty
// string means the field is missing or blank) and a row passes when, for every
// non-empty list, it matches one of that list's values. Lists never combine
// across fields with OR.
//
// UserIDMissing exists because a JSON list of integers cannot say "no user".
// Text values are the capped, display-ready form the breakdown reports: the
// metadata caps in the store apply before comparison, and StatusCodes and
// PollingTypes are compared after trimming whitespace.
//
// The summary (Analyze) does not apply this filter yet. It scopes the
// breakdown and the request drill-down only.
type RecordFilter struct {
	UserIDs       []int    `json:"userIds,omitempty"`
	UserIDMissing bool     `json:"userIdMissing,omitempty"`
	UIVersions    []string `json:"uiVersions,omitempty"`
	UserAgents    []string `json:"userAgents,omitempty"`
	UserIPs       []string `json:"userIps,omitempty"`
	StatusCodes   []string `json:"statusCodes,omitempty"`
	PollingTypes  []string `json:"pollingTypes,omitempty"`
}

// recordFilterCaps is the longest value, in code points, each text list accepts.
// It equals the store's metadata cap for the field, so a value read from a row
// always fits.
var recordFilterCaps = map[string]int{
	"uiVersions": 128, "userAgents": 512, "userIps": 64, "statusCodes": 128, "pollingTypes": 128,
}

// IsZero reports whether the filter selects every row.
func (f RecordFilter) IsZero() bool {
	return len(f.UserIDs) == 0 && !f.UserIDMissing && len(f.UIVersions) == 0 && len(f.UserAgents) == 0 &&
		len(f.UserIPs) == 0 && len(f.StatusCodes) == 0 && len(f.PollingTypes) == 0
}

// Validate bounds list sizes and value lengths.
func (f RecordFilter) Validate() error {
	if len(f.UserIDs) > MaxRecordFilterValues {
		return invalidInputf("userIds must contain at most %d values", MaxRecordFilterValues)
	}
	for name, values := range map[string][]string{
		"uiVersions": f.UIVersions, "userAgents": f.UserAgents, "userIps": f.UserIPs,
		"statusCodes": f.StatusCodes, "pollingTypes": f.PollingTypes,
	} {
		if len(values) > MaxRecordFilterValues {
			return invalidInputf("%s must contain at most %d values", name, MaxRecordFilterValues)
		}
		for _, value := range values {
			if utf8.RuneCountInString(value) > recordFilterCaps[name] {
				return invalidInputf("%s values must be at most %d characters", name, recordFilterCaps[name])
			}
		}
	}
	return nil
}

// Matches applies the filter to one row. Text fields are compared exactly,
// except status and polling type, which compare trimmed.
func (f RecordFilter) Matches(r RequestRecord) bool {
	if len(f.UserIDs) > 0 || f.UserIDMissing {
		ok := r.UserID == nil && f.UserIDMissing
		if r.UserID != nil {
			for _, id := range f.UserIDs {
				ok = ok || id == *r.UserID
			}
		}
		if !ok {
			return false
		}
	}
	return textMatches(f.UIVersions, r.UIVersion) && textMatches(f.UserAgents, r.UserAgent) &&
		textMatches(f.UserIPs, r.UserIP) && textMatches(f.StatusCodes, strings.TrimSpace(r.Status)) &&
		textMatches(f.PollingTypes, strings.TrimSpace(r.PollingTypeRaw))
}

func textMatches(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}
