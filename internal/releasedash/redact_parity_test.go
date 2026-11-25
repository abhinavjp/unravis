package releasedash

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The same table of split-line secrets that internal/secretscrub and tools/release/Test-ReleaseManager.ps1 read. The Redactor wraps the
// shared Carry, so a form that the scrubber hides must be hidden here as well, line by line.
func TestRedactorParityCases(t *testing.T) {
	raw, err := os.ReadFile("../secretscrub/testdata/parity_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var pc struct {
		Cases []struct {
			Name    string   `json:"name"`
			Lines   []string `json:"lines"`
			Secrets []string `json:"secrets"`
		} `json:"cases"`
		ExpectUnchanged []struct {
			Name  string   `json:"name"`
			Lines []string `json:"lines"`
		} `json:"expectUnchanged"`
	}
	if err := json.Unmarshal(raw, &pc); err != nil {
		t.Fatal(err)
	}
	if len(pc.Cases) == 0 || len(pc.ExpectUnchanged) == 0 {
		t.Fatal("the parity table is empty")
	}
	for _, c := range pc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := NewRedactor()
			var out []string
			for _, line := range c.Lines {
				out = append(out, r.Line(line))
			}
			got := strings.Join(out, "\n")
			for _, secret := range c.Secrets {
				if strings.Contains(got, secret) {
					t.Errorf("%q survived: %q", secret, got)
				}
			}
		})
	}
	for _, c := range pc.ExpectUnchanged {
		t.Run("unchanged "+c.Name, func(t *testing.T) {
			r := NewRedactor()
			for _, line := range c.Lines {
				if got := r.Line(line); got != line {
					t.Errorf("%q came out as %q", line, got)
				}
			}
		})
	}
}
