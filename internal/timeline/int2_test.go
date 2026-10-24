package timeline

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
)

// The app-session kind (ANCM stdout, one point per file) has its own plain
// phrase, starts with a consonant and gives a sentence that passes the
// causal-language lint.
func TestINT2AppSessionPhrase(t *testing.T) {
	got := eventPhrase(evidence.Event{Kind: "app-session"})
	if got != "session marker" {
		t.Fatalf("eventPhrase(app-session) = %q", got)
	}
	if strings.ContainsAny(got[:1], "aeiou") {
		t.Errorf("phrase %q starts with a vowel", got)
	}
	s := evidence.OverlapUnique(displayName("ancm-stdout"), got, 2)
	if err := evidence.LintCausal(s); err != nil {
		t.Errorf("sentence %q: %v", s, err)
	}
	if strings.Contains(s, "app-session") {
		t.Errorf("sentence %q holds the raw kind", s)
	}
}
