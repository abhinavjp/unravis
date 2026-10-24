package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestLintCausal(t *testing.T) {
	bad := []string{
		"the stall caused it", "This was Caused by x", "root cause", "because of the lock", "Because",
		"waiting on a lock", "waiting   on", "blocked by x", "due to load", "Proven", "proven cause",
		"PRОVEN",               // Cyrillic O
		"pro​ven",              // zero-width space
		"ｂecause",              // full-width b
		"it is causing delays", // any form of cause
	}
	for _, s := range bad {
		if err := LintCausal(s); !errors.Is(err, ErrCausal) {
			t.Errorf("LintCausal(%q) = %v, want ErrCausal", s, err)
		}
	}
	good := []string{
		"", "Symptom", "Correlation", "provenance of the file", "Possible overlap (ambiguous: 3 candidates).",
		OverlapUnique("sql-a", "wait", 2.5), AuditCreate(7), NotObservedGap("sampling"), NotObservedThreshold("x", "waits over 100 ms"),
		"overlapping this interval", "wait for lock",
	}
	for _, s := range good {
		if err := LintCausal(s); err != nil {
			t.Errorf("LintCausal(%q) = %v", s, err)
		}
	}
	if err := LintCausal("SENTINEL-7f3a because"); err != nil && strings.Contains(err.Error(), "SENTINEL") {
		t.Error("error leaks surrounding text")
	}
}
