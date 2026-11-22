package releasedash

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Test results come from the saved GitHub Actions run logs (ci, release and verify runs). Every step of every job is read for the
// four shapes of test output this project produces:
//
//	go test:         "ok<TAB>pkg<TAB>1.2s", "FAIL<TAB>pkg", "--- FAIL: TestName (0.00s)" and (-v) "--- PASS:" / "--- SKIP:"
//	node --test:     "ok 3 - name", "not ok 4 - name" and the "# pass 12" summary
//	verify script:   "PASS A1 what it checked", "FAIL A1 : why"
//	script tests:    "ok   name", "FAIL name detail" (Test-ReleaseManager.ps1 and the other PowerShell test scripts)
//
// A step whose name contains "report only" is shown as report only: it never fails the release.

// TestFailure is one failed test with the lines it printed.
type TestFailure struct {
	Name   string   `json:"name"`
	Output []string `json:"output,omitempty"`
}

// TestCase is one test or check by name. Only a limited number are kept per suite.
type TestCase struct {
	Name   string `json:"name"`
	Status string `json:"status"` // passed, failed or skipped
}

// Suite is the result of one step of one job.
type Suite struct {
	Job        string        `json:"job"`
	Step       string        `json:"step"`
	Kind       string        `json:"kind"` // go, node, verify or script
	Unit       string        `json:"unit"` // tests, packages or checks
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Skipped    int           `json:"skipped"`
	ReportOnly bool          `json:"reportOnly,omitempty"`
	Failures   []TestFailure `json:"failures,omitempty"`
	Cases      []TestCase    `json:"cases,omitempty"`
	CasesCut   bool          `json:"casesCut,omitempty"`

	// fromSummary: every result came from a "N --- FAIL" count summary, which only a report-only step prints.
	fromSummary bool
}

// TestResults is every suite found in one run log, plus the totals of the real ones (report only and package counts are left out).
type TestResults struct {
	Suites  []Suite `json:"suites"`
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	Skipped int     `json:"skipped"`
	// ReusedCI is the id of the CI run whose passed tests the release build trusted instead of running them again (the step "Did CI
	// already pass on this exact commit?" in release.yml). Set only when the log says so, so the page can say "skipped on purpose".
	ReusedCI string `json:"reusedCi,omitempty"`
}

const (
	maxCases      = 300
	maxFailures   = 60
	maxFailLines  = 40
	maxGroupLines = 150000
)

var (
	stampRE   = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z ?`)
	goPkgRE   = regexp.MustCompile(`^(ok|FAIL|\?)\s*\t\s*(\S+)\s*(.*)$`)
	goTestRE  = regexp.MustCompile(`^(\s*)--- (PASS|FAIL|SKIP): (\S+)`)
	goSumRE   = regexp.MustCompile(`^\s*(\d+)\s+--- FAIL: (\S+)`)
	tapRE     = regexp.MustCompile(`^(not )?ok (\d+) - (.*?)(?: # (SKIP|skip|TODO|todo).*)?$`)
	tapSumRE  = regexp.MustCompile(`^# (tests|pass|fail|cancelled|skipped|todo) (\d+)$`)
	passRE    = regexp.MustCompile(`^PASS (\S+) (.*)$`)
	failRE    = regexp.MustCompile(`^FAIL (\S+) ?: ?(.*)$`)
	scriptOK  = regexp.MustCompile(`^ok {2,}(\S.*)$`)
	scriptBad = regexp.MustCompile(`^FAIL +(\S.*)$`)
	// The line release.yml prints when it skips the tests. Anchored, so the echoed script text (which contains it too) does not count.
	reusedCIRE = regexp.MustCompile(`^CI run (\d{1,20}) passed on this commit: tests are skipped here`)
)

type logGroup struct {
	job, step string
	raw       []string
	red       []string
}

// ParseTests reads a saved CI log (the "job<TAB>step<TAB>time text" form of gh run view --log) and returns what it can find.
func ParseTests(r io.Reader, names []string) TestResults {
	res := TestResults{Suites: []Suite{}}
	var order []*logGroup
	byKey := map[string]*logGroup{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	nameRed := NewRedactor(names...)
	reds := map[*logGroup]*Redactor{}
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		key := parts[0] + "\x00" + parts[1]
		g := byKey[key]
		if g == nil {
			g = &logGroup{job: nameRed.Line(parts[0]), step: nameRed.Line(parts[1])}
			byKey[key] = g
			order = append(order, g)
			reds[g] = NewRedactor(names...)
		}
		if len(g.raw) >= maxGroupLines {
			continue
		}
		s := strings.TrimPrefix(parts[2], bomMark)
		s = ansiRE.ReplaceAllString(s, "")
		s = stampRE.ReplaceAllString(s, "")
		if m := reusedCIRE.FindStringSubmatch(s); m != nil {
			res.ReusedCI = m[1]
		}
		g.raw = append(g.raw, strings.TrimRight(s, "\r"))
		g.red = append(g.red, reds[g].Line(s))
	}
	for _, g := range order {
		for _, parse := range []func(*logGroup) *Suite{parseGo, parseNode, parseVerify, parseScript} {
			if s := parse(g); s != nil {
				// gh may print "UNKNOWN STEP" instead of the step name, so a count summary also marks a suite as report only.
				s.ReportOnly = strings.Contains(strings.ToLower(g.step), "report only") || s.fromSummary
				res.Suites = append(res.Suites, *s)
				if !s.ReportOnly && s.Unit != "packages" {
					res.Passed += s.Passed
					res.Failed += s.Failed
					res.Skipped += s.Skipped
				}
				break
			}
		}
	}
	return res
}

// unknownStepWord names a suite when gh could not say which step printed it (it prints "UNKNOWN STEP", as it does for the whole
// of a sharded job): the kind of suite is still known from its output.
var unknownStepWord = map[string]string{"go": "Go tests", "node": "Web tests", "verify": "Install check", "script": "Script tests"}

func (g *logGroup) suite(kind string) *Suite {
	step := g.step
	if strings.EqualFold(strings.TrimSpace(step), "UNKNOWN STEP") && unknownStepWord[kind] != "" {
		step = unknownStepWord[kind]
	}
	return &Suite{Job: g.job, Step: step, Kind: kind}
}

func addCase(s *Suite, name, status string) {
	if len(s.Cases) >= maxCases {
		s.CasesCut = true
		return
	}
	s.Cases = append(s.Cases, TestCase{Name: name, Status: status})
}

func addFailure(s *Suite, f TestFailure) {
	if len(s.Failures) < maxFailures {
		s.Failures = append(s.Failures, f)
	}
}

// indented returns the output lines that follow line i while they are indented or empty, up to the cap.
func indented(g *logGroup, i int, minIndent int) []string {
	var out []string
	for j := i + 1; j < len(g.raw) && len(out) < maxFailLines; j++ {
		t := g.raw[j]
		if strings.TrimSpace(t) == "" || len(t)-len(strings.TrimLeft(t, " \t")) < minIndent {
			break
		}
		out = append(out, strings.TrimSpace(g.red[j]))
	}
	return out
}

func parseGo(g *logGroup) *Suite {
	s := g.suite("go")
	var pkgOK, pkgFail, tests, sums int
	hdrRun := -1 // index of the last "=== RUN" line: output printed before a "--- FAIL" line follows it
	for i, t := range g.raw {
		if strings.HasPrefix(t, "=== RUN") {
			hdrRun = i
			continue
		}
		if m := goSumRE.FindStringSubmatch(t); m != nil { // the race report: "  3 --- FAIL: TestX (0.00s)"
			tests++
			s.Failed++
			sums++
			addFailure(s, TestFailure{Name: m[2], Output: []string{m[1] + " time(s) in the run"}})
			addCase(s, m[2], "failed")
			continue
		}
		if m := goTestRE.FindStringSubmatch(t); m != nil {
			tests++
			switch m[2] {
			case "PASS":
				s.Passed++
				addCase(s, m[3], "passed")
			case "SKIP":
				s.Skipped++
				addCase(s, m[3], "skipped")
			case "FAIL":
				s.Failed++
				addCase(s, m[3], "failed")
				out := indented(g, i, 4)
				if len(out) == 0 && hdrRun >= 0 && i-hdrRun > 1 && i-hdrRun <= maxFailLines {
					for _, l := range g.red[hdrRun+1 : i] {
						out = append(out, strings.TrimSpace(l))
					}
				}
				addFailure(s, TestFailure{Name: m[3], Output: out})
			}
			continue
		}
		if m := goPkgRE.FindStringSubmatch(t); m != nil {
			switch m[1] {
			case "ok":
				pkgOK++
			case "FAIL":
				pkgFail++
			}
		}
	}
	switch {
	case tests > 0:
		s.Unit = "tests"
		s.fromSummary = sums == tests
	case pkgOK+pkgFail > 0:
		s.Unit, s.Passed, s.Failed = "packages", pkgOK, pkgFail
	default:
		return nil
	}
	if s.Unit == "packages" && pkgFail > 0 { // "FAIL<TAB>pkg" without a test line: say which package
		for i, t := range g.raw {
			if m := goPkgRE.FindStringSubmatch(t); m != nil && m[1] == "FAIL" {
				addFailure(s, TestFailure{Name: NewRedactor().Line(m[2]), Output: append([]string{strings.TrimSpace(g.red[i])}, indented(g, i, 1)...)})
			}
		}
	}
	return s
}

func parseNode(g *logGroup) *Suite {
	s := g.suite("node")
	s.Unit = "tests"
	var sum = map[string]int{}
	seen := false
	for i, t := range g.raw {
		if m := tapSumRE.FindStringSubmatch(t); m != nil {
			n, _ := strconv.Atoi(m[2])
			sum[m[1]] = n
			seen = true
			continue
		}
		m := tapRE.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		seen = true
		name := NewRedactor().Line(m[3])
		switch {
		case m[1] != "":
			s.Failed++
			var out []string
			for j := i + 1; j < len(g.raw) && len(out) < maxFailLines; j++ {
				if strings.HasPrefix(g.raw[j], "ok ") || strings.HasPrefix(g.raw[j], "not ok ") || strings.HasPrefix(g.raw[j], "# ") {
					break
				}
				if l := strings.TrimSpace(g.red[j]); l != "" && l != "---" && l != "..." {
					out = append(out, l)
				}
			}
			addFailure(s, TestFailure{Name: name, Output: out})
			addCase(s, name, "failed")
		case m[4] != "":
			s.Skipped++
			addCase(s, name, "skipped")
		default:
			s.Passed++
			addCase(s, name, "passed")
		}
	}
	if !seen {
		return nil
	}
	if _, ok := sum["tests"]; ok { // the summary counts every test, the "ok" lines only the ones printed
		s.Passed, s.Failed, s.Skipped = sum["pass"], sum["fail"], sum["skipped"]+sum["todo"]
	}
	return s
}

func parseVerify(g *logGroup) *Suite {
	s := g.suite("verify")
	s.Unit = "checks"
	rd := NewRedactor()
	for i, t := range g.raw {
		if m := passRE.FindStringSubmatch(t); m != nil {
			s.Passed++
			addCase(s, m[1]+"  "+rd.Line(m[2]), "passed")
		} else if m := failRE.FindStringSubmatch(t); m != nil {
			s.Failed++
			name := m[1] + "  " + rd.Line(m[2])
			addCase(s, name, "failed")
			addFailure(s, TestFailure{Name: name, Output: []string{strings.TrimSpace(g.red[i])}})
		}
	}
	if s.Passed+s.Failed == 0 {
		return nil
	}
	return s
}

func parseScript(g *logGroup) *Suite {
	s := g.suite("script")
	s.Unit = "tests"
	rd := NewRedactor()
	for i, t := range g.raw {
		if m := scriptOK.FindStringSubmatch(t); m != nil {
			s.Passed++
			addCase(s, rd.Line(m[1]), "passed")
		} else if m := scriptBad.FindStringSubmatch(t); m != nil {
			s.Failed++
			name := rd.Line(m[1])
			addCase(s, name, "failed")
			addFailure(s, TestFailure{Name: name, Output: []string{strings.TrimSpace(g.red[i])}})
		}
	}
	if s.Passed+s.Failed == 0 {
		return nil
	}
	return s
}

// bomMark is the byte order mark GitHub puts at the start of the first line of each step.
var bomMark = string(rune(0xFEFF))
