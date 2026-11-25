package secretscrub

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// parityCases is the table that the PowerShell twin of this package (tools/release/ReleaseLib.ps1) reads too, from
// tools/release/Test-ReleaseManager.ps1. A line that one of them lets through and the other masks shows up here and there.
type parityCases struct {
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

func loadParityCases(t *testing.T) parityCases {
	t.Helper()
	raw, err := os.ReadFile("testdata/parity_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var pc parityCases
	if err := json.Unmarshal(raw, &pc); err != nil {
		t.Fatal(err)
	}
	if len(pc.Cases) == 0 || len(pc.ExpectUnchanged) == 0 {
		t.Fatal("the parity table is empty")
	}
	return pc
}

func TestCarryParityCases(t *testing.T) {
	pc := loadParityCases(t)
	for _, c := range pc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var carry Carry
			var out []string
			for _, line := range c.Lines {
				out = append(out, carry.Line(line, m))
			}
			got := strings.Join(out, "\n")
			for _, secret := range c.Secrets {
				if strings.Contains(got, secret) {
					t.Errorf("%q survived: %q", secret, shown(got))
				}
			}
		})
	}
	for _, c := range pc.ExpectUnchanged {
		t.Run("unchanged "+c.Name, func(t *testing.T) {
			var carry Carry
			for _, line := range c.Lines {
				if got := carry.Line(line, m); got != line {
					t.Errorf("%q came out as %q", line, shown(got))
				}
			}
		})
	}
}

// A stray "<a" at the end of a line is not a tag. The lines after it were read again together with all the lines before it
// (review N1): the cost of a run of lines grew with its square. The work is the size of the tag text Carry holds, so that is
// what is checked; the time is a loose guard on top.
func TestCarryStrayTagCostIsBounded(t *testing.T) {
	var c Carry
	most := 0
	start := time.Now()
	for i := 0; i < 100000; i++ {
		line := "o"
		if i%2000 == 0 {
			line = "x <a"
		}
		c.Line(line, m)
		if len(c.tag) > most {
			most = len(c.tag)
		}
	}
	if limit := (maxTagLines + 1) * 8; most > limit {
		t.Errorf("Carry held %d bytes of tag text, want at most %d", most, limit)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("100000 lines took %v", d)
	}
	// And the tag is let go: the line after the limit is read as new text.
	c = Carry{}
	c.Line("x <a", m)
	for i := 0; i < maxTagLines+1; i++ {
		c.Line("o", m)
	}
	if c.pend != pendNone {
		t.Errorf("the tag outlived its line limit (state %d)", c.pend)
	}
	if got := c.Line("password=FAKEPW", m); strings.Contains(got, "FAKEPW") {
		t.Errorf("a line after the limit was not read as new text: %q", got)
	}
}
