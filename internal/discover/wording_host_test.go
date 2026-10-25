package discover

import (
	"regexp"
	"strings"
	"testing"
)

// TestHostWordingLint applies the same sentence rules as TestWordingLint to the
// keys in wording_host.go, which are not in the golden file yet.
func TestHostWordingLint(t *testing.T) {
	check := func(k, text string) {
		t.Helper()
		for _, problem := range lintSentence(text) {
			t.Errorf("%s: %s (grade %.1f): %q", k, problem, fleschKincaid(text), text)
		}
		if strings.TrimSpace(text) != text || !strings.HasSuffix(text, ".") {
			t.Errorf("%s: sentence must be trimmed and end with a full stop", k)
		}
	}
	shape := regexp.MustCompile(`^discover\.[a-z0-9]+\.(shows|how)$`)
	for k, text := range hostShows {
		check(k, text)
		if !shape.MatchString(k) || !strings.HasSuffix(k, ".shows") {
			t.Errorf("host shows key %q has the wrong shape", k)
		}
	}
	for k, text := range hostHow {
		check(k, text)
		if !shape.MatchString(k) || !strings.HasSuffix(k, ".how") {
			t.Errorf("host how key %q has the wrong shape", k)
		}
	}
	for _, k := range []string{"discover.nettrace.shows", "discover.nettrace.how"} {
		if _, ok := hostWordingText(k); !ok {
			t.Errorf("%s has no wording", k)
		}
	}
}

// TestWebShowKeysResolve checks that every Shows key the web detector sets has
// wording in wording.go.
func TestWebShowKeysResolve(t *testing.T) {
	for _, k := range webShowKeys {
		if _, ok := ShowsText(k); !ok {
			t.Errorf("web Shows key %q has no wording", k)
		}
	}
}
