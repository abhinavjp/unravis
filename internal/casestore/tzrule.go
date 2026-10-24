package casestore

import (
	"sort"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

// DerivedTZRule is the time zone rule a file is recorded under. A rule the
// caller set wins. Otherwise it is read from the importer's own report
// (Report.FieldRules): the one rule every timestamp field of the file used.
// When the fields used several rules the strictest is recorded, host-local,
// because it is the one that needs a host label before the timeline will align
// the file; with no host-local field the first rule in name order is recorded.
// Values that are not a clock.Rule* constant are ignored, so a file with no
// known rule is recorded with none and the timeline stays off for its source.
func (m ImportMeta) DerivedTZRule(rep importer.Report) string {
	if m.TZRule != "" {
		return m.TZRule
	}
	var rules []string
	seen := map[string]bool{}
	for _, r := range rep.FieldRules {
		switch r {
		case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias, clock.RuleHostLocal:
			if !seen[r] {
				seen[r] = true
				rules = append(rules, r)
			}
		}
	}
	if len(rules) == 0 {
		return ""
	}
	if seen[clock.RuleHostLocal] {
		return clock.RuleHostLocal
	}
	sort.Strings(rules)
	return rules[0]
}
