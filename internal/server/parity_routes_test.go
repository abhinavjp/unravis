package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"api-audit-explorer/internal/clicap"
)

// parityFile classifies every UI route as covered by a CLI op, planned, or UI-only (docs/ops/CLI-PARITY-PLAN.md, OPS-01).
// A new route with no line here fails TestRoutesHaveParityEntry, so a UI feature cannot ship without a CLI decision.
var parityFile = filepath.Join("testdata", "parity-routes.txt")

// planIDPattern matches a plan item id of the plan: OPS-11, OPS-F1, OPS-S1.
var planIDPattern = regexp.MustCompile(`^OPS-[A-Z]?\d+$`)

// routeKeys lists "METHOD pattern" for every method of every route in the routes table, sorted. The Blackbox routes are in the
// same table: internal/server/blackbox.go appends them to routes in init, so no route is skipped.
func routeKeys() []string {
	var keys []string
	for _, rt := range routes {
		for method := range rt.methods {
			keys = append(keys, method+" "+rt.pattern)
		}
	}
	sort.Strings(keys)
	return keys
}

// checkParity compares the route keys with the text of the parity file and returns one sorted problem line per defect: a route with
// no line, a line whose route is gone, a malformed or duplicate line, a bad status, a cli op that is not in the capability matrix,
// a planned line with no plan id, a ui-only line with no reason. No problems returns nil.
func checkParity(routeKeys []string, fileText string) []string {
	have := make(map[string]bool, len(routeKeys))
	for _, k := range routeKeys {
		have[k] = true
	}
	seen := map[string]bool{}
	var problems []string
	for i, line := range strings.Split(fileText, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		where := fmt.Sprintf("line %d", i+1)
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			problems = append(problems, fmt.Sprintf("%s: want 3 tab-separated columns (METHOD pattern, status, op or reason), got %d: %q", where, len(cols), line))
			continue
		}
		key, status, third := cols[0], cols[1], strings.TrimSpace(cols[2])
		if seen[key] {
			problems = append(problems, fmt.Sprintf("%s: duplicate entry for %q", where, key))
		}
		seen[key] = true
		if !have[key] {
			problems = append(problems, fmt.Sprintf("%s: route no longer exists: %q", where, key))
		}
		switch status {
		case "cli":
			if _, ok := clicap.Lookup(third); !ok {
				problems = append(problems, fmt.Sprintf("%s: %q is marked cli but op %q is not in clicap.Lookup", where, key, third))
			}
		case "planned":
			if !planIDPattern.MatchString(third) {
				problems = append(problems, fmt.Sprintf("%s: %q is marked planned but %q is not a plan item id such as OPS-11", where, key, third))
			}
		case "ui-only":
			if third == "" {
				problems = append(problems, fmt.Sprintf("%s: %q is marked ui-only with no reason", where, key))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: %q has bad status %q (want cli, planned or ui-only)", where, key, status))
		}
	}
	for _, k := range routeKeys {
		if !seen[k] {
			problems = append(problems, fmt.Sprintf("route missing from %s: %q", filepath.ToSlash(parityFile), k))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestRoutesHaveParityEntry(t *testing.T) {
	data, err := os.ReadFile(parityFile) //nolint:gosec // a fixed testdata file
	if err != nil {
		t.Fatalf("read parity table: %v", err)
	}
	if problems := checkParity(routeKeys(), string(data)); len(problems) > 0 {
		t.Fatalf("UI routes and %s disagree (every route needs a cli, planned or ui-only decision):\n  %s",
			filepath.ToSlash(parityFile), strings.Join(problems, "\n  "))
	}
}

// TestCheckParityMutants proves the checker notices each kind of defect, so the table test cannot pass by accident.
func TestCheckParityMutants(t *testing.T) {
	data, err := os.ReadFile(parityFile) //nolint:gosec // a fixed testdata file
	if err != nil {
		t.Fatalf("read parity table: %v", err)
	}
	keys := routeKeys()
	if len(keys) == 0 {
		t.Fatal("routes table is empty")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got := checkParity(keys, strings.ReplaceAll(text, "\n", "\r\n")); len(got) != 0 {
		t.Fatalf("CRLF file text should be accepted, got %v", got)
	}

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	without := func(drop int) string {
		return strings.Join(append(append([]string{}, lines[:drop]...), lines[drop+1:]...), "\n")
	}
	expect := func(name string, problems []string, want string) {
		t.Helper()
		for _, p := range problems {
			if strings.Contains(p, want) {
				return
			}
		}
		t.Errorf("%s: want a problem containing %q, got %v", name, want, problems)
	}

	// A route whose line was removed from the file.
	dropped := strings.SplitN(lines[0], "\t", 2)[0]
	expect("missing line", checkParity(keys, without(0)), "route missing from")
	expect("missing line key", checkParity(keys, without(0)), dropped)

	// A line whose route was removed from the table.
	expect("stale line", checkParity(keys[1:], text), "route no longer exists")

	// A bad status, an op that is not in the matrix, a planned line without a plan id, a ui-only line without a reason.
	expect("bad status", checkParity(keys, text+"\nGET /x\tmaybe\tx"), "bad status")
	expect("bad op", checkParity(keys, text+"\nGET /x\tcli\tno.such.op"), "not in clicap.Lookup")
	expect("bad plan id", checkParity(keys, text+"\nGET /x\tplanned\tsoon"), "not a plan item id")
	expect("no reason", checkParity(keys, text+"\nGET /x\tui-only\t"), "no reason")
	expect("duplicate", checkParity(keys, text+"\n"+lines[0]), "duplicate entry")

	// Output is sorted, so messages are stable.
	got := checkParity(keys, text+"\nGET /b\tmaybe\tx\nGET /a\tmaybe\tx")
	if !sort.StringsAreSorted(got) {
		t.Errorf("problems are not sorted: %v", got)
	}
}
