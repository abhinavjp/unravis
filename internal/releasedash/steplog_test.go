package releasedash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRunLog(t *testing.T, rows []string) (dir, name string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, logsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	name = "run-20261122T114405Z.log"
	body := strings.Join(rows, "\r\n") + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, logsDir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, name
}

func TestFixMojibake(t *testing.T) {
	for in, want := range map[string]string{
		"Γ£ô Set up job":  "✓ Set up job",
		"a ┬╖ b":          "a · b",
		"plain ascii":     "plain ascii",
		"café":            "café", // honest text: not valid UTF-8 once mapped back, so left alone
		"Γ":               "Γ",
		"✓ already right": "✓ already right",
	} {
		if got := fixMojibake(in); got != want {
			t.Errorf("fixMojibake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadLogRepairsAndFoldsStatusBlocks(t *testing.T) {
	const ts = "2026-11-22T11:45:0"
	rows := []string{ts + "0Z [5/7] Wait for the release build"}
	for i := 1; i <= 4; i++ {
		rows = append(rows,
			fmt.Sprintf("%s%dZ Refreshing run status every 10 seconds. Press Ctrl+C to quit.", ts, i),
			fmt.Sprintf("%s%dZ ", ts, i),
			fmt.Sprintf("%s%dZ JOBS", ts, i),
			fmt.Sprintf("%s%dZ   Γ£ô step %d", ts, i, i))
	}
	rows = append(rows, ts+"9Z [6/7] Verify", ts+"9Z Refreshing run status every 10 seconds. Press Ctrl+C to quit.", ts+"9Z only block of the second watch")
	dir, name := writeRunLog(t, rows)
	lines, _, err := ReadLog(dir, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.Text)
	}
	all := strings.Join(texts, "\n")
	if strings.Contains(all, "Γ") || !strings.Contains(all, "✓ step 4") {
		t.Fatalf("mojibake not repaired:\n%s", all)
	}
	if strings.Contains(all, "step 1") || strings.Contains(all, "step 3") || !strings.Contains(all, "3 earlier copies") {
		t.Fatalf("earlier blocks not folded:\n%s", all)
	}
	if n := strings.Count(all, "Refreshing run status"); n != 2 {
		t.Fatalf("want the last block of each watch (2), got %d:\n%s", n, all)
	}
	// Headings still slice and line numbers refer to the returned lines.
	steps := SliceSteps(lines)
	if len(steps) != 2 || !strings.HasPrefix(lines[steps[1].From].Text, "[6/7]") {
		t.Fatalf("steps after folding: %+v", steps)
	}
}
