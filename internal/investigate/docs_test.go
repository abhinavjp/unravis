package investigate

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode"

	"api-audit-explorer/internal/evidence"
)

const guidedDocPath = "../../docs/user/guided-analysis.md"

func readGuidedDoc(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(guidedDocPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
}

// Every step has a section with its fixed name and its "In a nutshell" box, word for
// word as the engine's step card shows it.
func TestGuidedAnalysisDocHasEveryStepNutshell(t *testing.T) {
	lines := readGuidedDoc(t)
	for n := 0; n < StepCount; n++ {
		heading := fmt.Sprintf("## Step %d. %s", n, StepName(n))
		at := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == heading {
				at = i
				break
			}
		}
		if at < 0 {
			t.Errorf("no section %q", heading)
			continue
		}
		want := "> **In a nutshell:** " + StepNutshell(n)
		found := false
		// The box sits right under the heading (one blank line allowed).
		for _, l := range lines[at+1 : min(at+4, len(lines))] {
			if strings.TrimSpace(l) == want {
				found = true
			}
		}
		if !found {
			t.Errorf("section %q has no box with the exact nutshell text", heading)
		}
	}
}

// The page uses the same words as the engine: no cause words, no percent sign, no
// emoji, and the threshold version it quotes is the current one.
func TestGuidedAnalysisDocWording(t *testing.T) {
	lines := readGuidedDoc(t)
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "%") {
		t.Errorf("the page holds a percent sign")
	}
	for i, l := range lines {
		if err := evidence.LintCausal(l); err != nil {
			t.Errorf("line %d: %q: %v", i+1, l, err)
		}
		for _, r := range l {
			if r > unicode.MaxASCII && (unicode.Is(unicode.So, r) || r >= 0x1F000) {
				t.Errorf("line %d holds a symbol or emoji %q", i+1, r)
			}
		}
	}
	if !strings.Contains(text, "The current version is "+Version+".") {
		t.Errorf("the page does not quote the current threshold version %s", Version)
	}
	for _, w := range []Readiness{Ready, PartlyReady, CannotTestYet, Failed} {
		if !strings.Contains(text, "| "+string(w)+" |") {
			t.Errorf("readiness word %q is not in the readiness table", w)
		}
	}
	for _, w := range []SuspectResult{ResConsistent, ResPartly, ResContra, ResCannotTest} {
		if !strings.Contains(text, "| "+string(w)+" |") {
			t.Errorf("suspect result %q is not in the results table", w)
		}
	}
}
