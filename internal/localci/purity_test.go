package localci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCorePurity keeps the core reusable: no project, company or machine-specific
// names in this package. Project specifics live in config or the CLI adapter.
func TestCorePurity(t *testing.T) {
	banned := regexp.MustCompile(`(?i)unravis|brain ?payroll|d:[\/]+secret`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "purity_test.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if banned.MatchString(line) {
				t.Errorf("%s:%d names a project-specific word: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
