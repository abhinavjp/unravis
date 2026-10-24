package casestore

import (
	"testing"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

func TestDerivedTZRule(t *testing.T) {
	rep := func(rules map[string]string) importer.Report { return importer.Report{FieldRules: rules} }
	cases := []struct {
		name string
		meta ImportMeta
		rep  importer.Report
		want string
	}{
		{"no report, no rule", ImportMeta{}, rep(nil), ""},
		{"one rule", ImportMeta{}, rep(map[string]string{"time": clock.RuleUTCField}), clock.RuleUTCField},
		{"same rule in two fields", ImportMeta{}, rep(map[string]string{"a": clock.RuleOffsetInField, "b": clock.RuleOffsetInField}), clock.RuleOffsetInField},
		{"host-local wins", ImportMeta{}, rep(map[string]string{"a": clock.RuleUTCField, "b": clock.RuleHostLocal}), clock.RuleHostLocal},
		{"first by name otherwise", ImportMeta{}, rep(map[string]string{"a": clock.RuleUTCField, "b": clock.RuleHeaderBias}), clock.RuleHeaderBias},
		{"unknown rule ignored", ImportMeta{}, rep(map[string]string{"a": "made-up"}), ""},
		{"caller rule wins", ImportMeta{TZRule: clock.RuleHostLocal}, rep(map[string]string{"a": clock.RuleUTCField}), clock.RuleHostLocal},
	}
	for _, c := range cases {
		if got := c.meta.DerivedTZRule(c.rep); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
