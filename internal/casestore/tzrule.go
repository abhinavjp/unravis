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

// DerivedHostLabel is the label the import is recorded under. The store calls
// it with the importer's report after the run: when every event of the file was
// filed under one label, that label is recorded (the file may name computers
// the person did not pick); when the events went to several labels, none is
// recorded, so the record never names one machine among them; and when the
// events carry no label at all (importers that tag no events), the host the
// person picked stands as before.
func (m ImportMeta) DerivedHostLabel(rep importer.Report) string {
	first := ""
	count := 0
	for _, l := range rep.HostLabels {
		if l == "" {
			continue
		}
		switch {
		case count == 0:
			first, count = l, 1
		case l != first:
			count = 2
		}
	}
	switch count {
	case 0:
		return m.HostLabel
	case 1:
		return first
	}
	return ""
}
