package casestore_test

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func TestValidNewTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{"empty", "", false},
		{"blank", "   ", false},
		{"one letter", "a", false},
		{"one letter in spaces", " a ", false},
		{"two letters", "ab", true},
		{"80 letters", strings.Repeat("a", 80), true},
		{"81 letters", strings.Repeat("a", 81), false},
		{"80 accented", strings.Repeat("é", 80), true},
		{"81 accented", strings.Repeat("é", 81), false},
		{"control character", "ab\x00", false},
		{"bidi override", "ab‮", false},
	}
	for _, c := range cases {
		if got := casestore.ValidNewTitle(c.title); got != c.want {
			t.Errorf("%s: ValidNewTitle = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOldLongTitlesStayValid(t *testing.T) {
	long := strings.Repeat("a", 150)
	if !casestore.ValidTitle(long) {
		t.Error("ValidTitle refused an old 150 character title")
	}
	if casestore.ValidNewTitle(long) {
		t.Error("ValidNewTitle accepted a 150 character title")
	}
}
