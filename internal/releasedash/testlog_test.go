package releasedash

import (
	"os"
	"strings"
	"testing"
)

func TestParseTestsCISample(t *testing.T) {
	f, err := os.Open("testdata/ci-sample.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res := ParseTests(f, nil)
	by := map[string]Suite{}
	for _, s := range res.Suites {
		by[s.Step] = s
	}
	goPk := by["Go - tests"]
	if goPk.Unit != "packages" || goPk.Failed != 0 || goPk.Passed < 10 {
		t.Fatalf("go packages: %+v", goPk)
	}
	pipe := by["Pipe server tests (BL-301)"]
	if pipe.Unit != "tests" || pipe.Passed < 2 || pipe.Failed != 0 {
		t.Fatalf("pipe: %+v", pipe)
	}
	race := by["Go - race detector (report only)"]
	if !race.ReportOnly || race.Failed < 30 || len(race.Failures) < 30 {
		t.Fatalf("race: failed=%d failures=%d reportOnly=%v", race.Failed, len(race.Failures), race.ReportOnly)
	}
	web := by["Web - tests"]
	if web.Kind != "node" || web.Passed != 3456 || web.Failed != 0 {
		t.Fatalf("web: %+v", web)
	}
	// report-only and package counts stay out of the totals
	if res.Failed != 0 || res.Passed < 3456 {
		t.Fatalf("totals: %+v", res)
	}
}

func TestParseTestsVerifySample(t *testing.T) {
	f, err := os.Open("testdata/verify-sample.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res := ParseTests(f, nil)
	var checks int
	for _, s := range res.Suites {
		if s.Kind == "verify" {
			checks += s.Passed
			if s.Failed != 0 {
				t.Fatalf("unexpected failure: %+v", s)
			}
		}
	}
	if checks < 8 {
		t.Fatalf("expected the PASS checks, got %d (%+v)", checks, res)
	}
}

func TestParseTestsFailures(t *testing.T) {
	log := strings.Join([]string{
		"job\tGo test\t2026-11-22T07:00:00.1Z === RUN   TestA",
		"job\tGo test\t2026-11-22T07:00:00.2Z     a_test.go:12: want 1 got 2 token=abc123456789",
		"job\tGo test\t2026-11-22T07:00:00.3Z --- FAIL: TestA (0.00s)",
		"job\tGo test\t2026-11-22T07:00:00.4Z === RUN   TestB",
		"job\tGo test\t2026-11-22T07:00:00.5Z --- PASS: TestB (0.00s)",
		"job\tGo test\t2026-11-22T07:00:00.6Z FAIL\tpkg/a\t0.1s",
		"job\tNode\t2026-11-22T07:00:01Z ok 1 - fine",
		"job\tNode\t2026-11-22T07:00:01Z not ok 2 - broken thing",
		"job\tNode\t2026-11-22T07:00:01Z   ---",
		"job\tNode\t2026-11-22T07:00:01Z   error: 'boom'",
		"job\tNode\t2026-11-22T07:00:01Z   ...",
		"job\tVerify\t2026-11-22T07:00:02Z PASS A1 feed ok",
		"job\tVerify\t2026-11-22T07:00:02Z FAIL B2 : did not upgrade",
	}, "\n")
	res := ParseTests(strings.NewReader(log), nil)
	if len(res.Suites) != 3 || res.Failed != 3 || res.Passed != 3 {
		t.Fatalf("%+v", res)
	}
	g := res.Suites[0]
	if len(g.Failures) != 1 || g.Failures[0].Name != "TestA" || len(g.Failures[0].Output) == 0 {
		t.Fatalf("go failure: %+v", g.Failures)
	}
	if strings.Contains(strings.Join(g.Failures[0].Output, "\n"), "abc123456789") {
		t.Fatalf("secret not hidden: %+v", g.Failures[0].Output)
	}
	n := res.Suites[1]
	if len(n.Failures) != 1 || !strings.Contains(strings.Join(n.Failures[0].Output, " "), "boom") {
		t.Fatalf("node failure: %+v", n.Failures)
	}
}

// gh sometimes prints "UNKNOWN STEP" for the step name; the race report must still stay out of the totals.
func TestParseTestsSummaryWithoutStepName(t *testing.T) {
	log := "linux-gates\tUNKNOWN STEP\t2026-11-22T07:33:31.4316889Z       2 --- FAIL: TestA (0.00s)\n" +
		"linux-gates\tUNKNOWN STEP\t2026-11-22T07:33:31.4317652Z       1 WARNING: DATA RACE\n" +
		"linux-gates\tUNKNOWN STEP\t2026-11-22T07:33:31.4318210Z       1 --- FAIL: TestB (0.00s)\n" +
		"build\tUNKNOWN STEP\t2026-11-22T07:28:55.1283356Z ok  \tpkg/a\t1.0s\n"
	res := ParseTests(strings.NewReader(log), nil)
	if res.Failed != 0 || len(res.Suites) != 2 {
		t.Fatalf("failed=%d suites=%d", res.Failed, len(res.Suites))
	}
	for _, s := range res.Suites {
		if s.Job == "linux-gates" && (!s.ReportOnly || s.Failed != 2) {
			t.Fatalf("race suite: %+v", s)
		}
	}
}
