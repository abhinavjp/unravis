package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The lint below is a port of the rules in web/src/lib/copy/copylint.ts that
// apply to a fixed sentence: causal phrases, diagnosis words and the
// Flesch-Kincaid grade limit.

const (
	maxGrade        = 8.5
	minWordsForGrad = 8
)

var causalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bcaus(?:e|es|ed|ing)\b`),
	regexp.MustCompile(`(?i)\bbecause\b`),
	regexp.MustCompile(`(?i)\bwaiting on\b`),
	regexp.MustCompile(`(?i)\bwait(?:s|ed|ing)? for\b`),
	regexp.MustCompile(`(?i)\bblocked by\b`),
	regexp.MustCompile(`(?i)\bdue to\b`),
	regexp.MustCompile(`(?i)\bthe database was waiting\b`),
	regexp.MustCompile(`(?i)\b(?:is|was|were|are)\s+the\s+reason\b`),
	regexp.MustCompile(`(?i)\b(?:led|leads|leading)\s+to\b`),
	regexp.MustCompile(`(?i)\bresult(?:ed|s|ing)\s+(?:from|in)\b`),
	regexp.MustCompile(`(?i)\bresponsible for\b`),
}

var diagnosisWords = regexp.MustCompile(`(?i)\b(?:leak|starv|saturat)\w*`)

var (
	wordRe        = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'’-]*`)
	sentenceSplit = regexp.MustCompile(`[.!?]+(?:\s|$)`)
	alnum         = regexp.MustCompile(`[A-Za-z0-9]`)
	nonLetter     = regexp.MustCompile(`[^a-z]`)
	suffixRe      = regexp.MustCompile(`(?:[^laeiouy]es|[^laeiouy]ed|[^laeiouy]e)$`)
	vowelGroup    = regexp.MustCompile(`[aeiouy]{1,2}`)
)

func syllablesInPart(part string) int {
	w := nonLetter.ReplaceAllString(strings.ToLower(part), "")
	if w == "" || len(w) <= 3 {
		return 1
	}
	stripped := strings.TrimPrefix(suffixRe.ReplaceAllString(w, ""), "y")
	n := len(vowelGroup.FindAllString(stripped, -1))
	if n < 1 {
		n = 1
	}
	return n
}

func countSyllables(word string) int {
	total := 0
	for _, p := range strings.Split(word, "-") {
		total += syllablesInPart(p)
	}
	return total
}

func fleschKincaid(text string) float64 {
	words := wordRe.FindAllString(text, -1)
	if len(words) == 0 {
		return 0
	}
	sentences := 0
	for _, s := range sentenceSplit.Split(text, -1) {
		if alnum.MatchString(s) {
			sentences++
		}
	}
	if sentences < 1 {
		sentences = 1
	}
	syl := 0
	for _, w := range words {
		syl += countSyllables(w)
	}
	return 0.39*float64(len(words))/float64(sentences) + 11.8*float64(syl)/float64(len(words)) - 15.59
}

func lintSentence(text string) []string {
	var out []string
	for _, re := range causalPatterns {
		if re.MatchString(text) {
			out = append(out, "causal phrase "+re.String())
		}
	}
	if diagnosisWords.MatchString(text) {
		out = append(out, "diagnosis word")
	}
	if len(wordRe.FindAllString(text, -1)) >= minWordsForGrad {
		if g := fleschKincaid(text); g > maxGrade {
			out = append(out, "reading grade too high")
		}
	}
	return out
}

func TestWordingLint(t *testing.T) {
	for _, k := range WordingKeys() {
		text, ok := WordingText(k)
		if !ok {
			t.Fatalf("%s: key listed with no text", k)
		}
		for _, problem := range lintSentence(text) {
			t.Errorf("%s: %s (grade %.1f): %q", k, problem, fleschKincaid(text), text)
		}
		if strings.TrimSpace(text) != text || !strings.HasSuffix(text, ".") {
			t.Errorf("%s: sentence must be trimmed and end with a full stop", k)
		}
	}
}

func TestWordingKeysShape(t *testing.T) {
	re := regexp.MustCompile(`^discover\.[a-z0-9]+\.(shows|how)$`)
	for _, k := range WordingKeys() {
		if !re.MatchString(k) {
			t.Errorf("key %q does not match discover.<source>.(shows|how)", k)
		}
		_, inShows := ShowsText(k)
		_, inHow := HowText(k)
		if strings.HasSuffix(k, ".shows") != inShows || strings.HasSuffix(k, ".how") != inHow {
			t.Errorf("key %q is in the wrong table", k)
		}
	}
	if _, ok := ShowsText("discover.unknown.shows"); ok {
		t.Error("unknown key has text")
	}
}

func TestLintCatchesBadCopy(t *testing.T) {
	bad := []string{
		"The slow disk is the reason requests are late and the app is waiting for it.",
		"This could be a memory leak in the app pool.",
		"The queue was caused by the database.",
		"Utilisation of the aforementioned instrumentation notwithstanding, comprehensive interpretation necessitates consideration.",
	}
	for _, s := range bad {
		if len(lintSentence(s)) == 0 {
			t.Errorf("lint passed %q", s)
		}
	}
}

type goldenEntry struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

func TestWordingGolden(t *testing.T) {
	var entries []goldenEntry
	for _, k := range WordingKeys() {
		text, _ := WordingText(k)
		entries = append(entries, goldenEntry{k, text})
	}
	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "wording.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
		t.Fatalf("wording changed; review it and run with UPDATE_GOLDEN=1\n--- want\n%s\n--- got\n%s", want, got)
	}
}
