package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func shaN(n int) string { return fmt.Sprintf("%040x", n) }

var cmpT0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// localPR is the index entry of a local run of a pull request into develop.
func localPR(pr int, sha, conclusion string, minute int) IndexEntry {
	return IndexEntry{RunID: fmt.Sprintf("run-%d-%s", pr, conclusion), Workflow: "quick.yml", Event: "pull_request", Repo: "o/r", PR: pr,
		BaseRef: "develop", HeadRef: "feature", HeadSHA: sha, Conclusion: conclusion, End: cmpT0.Add(time.Duration(minute) * time.Minute)}
}

func run(name, status, conclusion string) GHCheck {
	return GHCheck{Name: name, State: conclusion, Outcome: checkRunOutcome(status, conclusion)}
}

func stat(name, state string) GHCheck {
	return GHCheck{Name: name, State: state, Outcome: statusOutcome(state)}
}

func compareOnly(t *testing.T, idx []IndexEntry, src *FakeCompareSource, limit int) *CompareReport {
	t.Helper()
	// only the pull request gate: push gates would ask for branch tips the fake does not have
	n := StatusNaming{Gates: []GateRule{{Kind: KindPR, Branch: "develop", Workflow: "quick.yml", Context: "quick-gate"}}}
	rep, err := Compare(context.Background(), CompareOptions{Repo: "o/r", Index: idx, Source: src, Naming: n, Limit: limit, Now: func() time.Time { return cmpT0 }})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func rowFor(t *testing.T, rep *CompareReport, pr int) CompareRow {
	t.Helper()
	for _, r := range rep.Rows {
		if r.Number == pr {
			return r
		}
	}
	t.Fatalf("no row for pr %d in %+v", pr, rep.Rows)
	return CompareRow{}
}

func TestCompareVerdicts(t *testing.T) {
	idx := []IndexEntry{
		localPR(1, shaN(1), "success", 1),   // agree (pass, pass)
		localPR(2, shaN(2), "failure", 2),   // agree (fail, fail)
		localPR(3, shaN(3), "success", 3),   // disagree (local pass, github fail)
		localPR(4, shaN(4), "success", 4),   // local-only: github has nothing
		localPR(5, shaN(5), "success", 5),   // local-only: github still running
		localPR(6, shaN(6), "error", 6),     // local error, github has a result: disagree
		localPR(7, shaN(7), "failure", 7),   // disagree (local fail, github pass)
		localPR(8, shaN(8), "cancelled", 8), // moot, skipped
	}
	src := &FakeCompareSource{
		Pulls: []PullHead{{Number: 9, Base: "develop", SHA: shaN(9), State: "OPEN"}, {Number: 10, Base: "develop", SHA: shaN(10), State: "OPEN"}, {Number: 11, Base: "elsewhere", SHA: shaN(11)}},
		Results: map[string]GHChecks{
			shaN(1):  {Runs: []GHCheck{run("quick-gate", "completed", "success"), run("other", "completed", "failure")}},
			shaN(2):  {Runs: []GHCheck{run("quick-gate", "completed", "failure")}},
			shaN(3):  {Runs: []GHCheck{run("quick-gate", "completed", "failure")}},
			shaN(4):  {},
			shaN(5):  {Runs: []GHCheck{run("quick-gate", "in_progress", "")}},
			shaN(6):  {Runs: []GHCheck{run("quick-gate", "completed", "success")}},
			shaN(7):  {Runs: []GHCheck{run("quick-gate", "completed", "success")}},
			shaN(9):  {Runs: []GHCheck{run("quick-gate", "completed", "success")}}, // github-only
			shaN(10): {Runs: []GHCheck{run("quick-gate", "queued", "")}},           // nothing on either side: no row
		},
	}
	rep := compareOnly(t, idx, src, 30)
	want := map[int]string{1: VerdictAgree, 2: VerdictAgree, 3: VerdictDisagree, 4: VerdictLocalOnly, 5: VerdictLocalOnly, 6: VerdictDisagree, 7: VerdictDisagree, 9: VerdictGitHubOnly}
	for pr, v := range want {
		if got := rowFor(t, rep, pr).Verdict; got != v {
			t.Errorf("pr %d: %s, want %s", pr, got, v)
		}
	}
	for _, r := range rep.Rows {
		if r.Number == 8 || r.Number == 10 || r.Number == 11 {
			t.Errorf("pr %d must have no row: %+v", r.Number, r)
		}
	}
	if rep.Agree != 2 || rep.Disagree != 3 || rep.LocalOnly != 2 || rep.GitHubOnly != 1 {
		t.Errorf("counts %+v", rep)
	}
	if len(rep.Disagreeing) != 3 || !strings.Contains(strings.Join(rep.Disagreeing, "\n"), "pr 3 "+shaN(3)[:7]) {
		t.Errorf("disagreeing %v", rep.Disagreeing)
	}
	if n := rowFor(t, rep, 6).Note; !strings.Contains(n, "without a verdict") {
		t.Errorf("error note %q", n)
	}
	if rowFor(t, rep, 5).Note != "GitHub has not finished" {
		t.Errorf("pending note %q", rowFor(t, rep, 5).Note)
	}
}

func TestCompareStreakCountsPullRequestsInARowNewestFirst(t *testing.T) {
	var idx []IndexEntry
	results := map[string]GHChecks{}
	for i := 1; i <= 25; i++ {
		idx = append(idx, localPR(i, shaN(i), "success", i))
		results[shaN(i)] = GHChecks{Runs: []GHCheck{run("quick-gate", "completed", "success")}}
	}
	rep := compareOnly(t, idx, &FakeCompareSource{Results: results}, 100)
	if rep.Streak != 25 || !rep.GateMet || rep.Gate != CompareGate {
		t.Fatalf("streak %d met %v", rep.Streak, rep.GateMet)
	}
	// pr 10 disagrees: the streak is what came after it (11..25)
	results[shaN(10)] = GHChecks{Runs: []GHCheck{run("quick-gate", "completed", "failure")}}
	rep = compareOnly(t, idx, &FakeCompareSource{Results: results}, 100)
	if rep.Streak != 15 || rep.GateMet || rep.Disagree != 1 {
		t.Fatalf("streak %d met %v disagree %d", rep.Streak, rep.GateMet, rep.Disagree)
	}
	// a local-only pull request neither counts nor breaks it
	idx = append(idx, localPR(26, shaN(26), "success", 26))
	rep = compareOnly(t, idx, &FakeCompareSource{Results: results}, 100)
	if rep.Streak != 15 {
		t.Fatalf("local-only must be skipped: %d", rep.Streak)
	}
	// the limit caps how many are looked at
	rep = compareOnly(t, idx, &FakeCompareSource{Results: results}, 5)
	if len(rep.Rows) != 5 {
		t.Fatalf("limit: %d rows", len(rep.Rows))
	}
}

func TestCompareShowsBothACheckRunAndAStatusOfTheSameName(t *testing.T) {
	src := &FakeCompareSource{Results: map[string]GHChecks{
		shaN(1): {Runs: []GHCheck{run("quick-gate", "completed", "success")}, Statuses: []GHCheck{stat("quick-gate", "failure")}},
		shaN(2): {Runs: []GHCheck{run("quick-gate", "completed", "success")}, Statuses: []GHCheck{stat("quick-gate", "success"), stat("local/quick-gate", "failure")}},
		shaN(3): {Statuses: []GHCheck{stat("quick-gate", "pending")}},
	}}
	rep := compareOnly(t, []IndexEntry{localPR(1, shaN(1), "success", 1), localPR(2, shaN(2), "success", 2), localPR(3, shaN(3), "success", 3)}, src, 30)
	r1 := rowFor(t, rep, 1)
	if r1.Check == nil || r1.Status == nil || r1.GitHub != OutcomeFail || r1.Verdict != VerdictDisagree || !strings.Contains(r1.Note, "differ") {
		t.Fatalf("both exist and a failure wins: %+v", r1)
	}
	r2 := rowFor(t, rep, 2)
	if r2.GitHub != OutcomePass || r2.Verdict != VerdictAgree {
		t.Fatalf("the local/ shadow status must not be read as the real one: %+v", r2)
	}
	if r3 := rowFor(t, rep, 3); r3.Status == nil || r3.Check != nil || r3.Verdict != VerdictLocalOnly {
		t.Fatalf("a status alone: %+v", r3)
	}
	var tbl bytes.Buffer
	rep.WriteTable(&tbl)
	for _, w := range []string{"pass / fail", "- / pending", "agree", "disagree", "pull requests in a row that agree: 1 of 20 needed"} {
		if !strings.Contains(tbl.String(), w) {
			t.Errorf("table lacks %q:\n%s", w, tbl.String())
		}
	}
}

func TestCompareUsesTheNewestLocalRunPerCommitAndWarnsOnSourceErrors(t *testing.T) {
	older := localPR(1, shaN(1), "failure", 1)
	newer := localPR(1, shaN(1), "success", 5)
	newer.RunID = "rerun"
	src := &FakeCompareSource{
		Results: map[string]GHChecks{shaN(1): {Runs: []GHCheck{run("quick-gate", "completed", "success")}}},
		Err:     map[string]error{shaN(2): errors.New("HTTP 502")},
	}
	rep := compareOnly(t, []IndexEntry{older, newer, localPR(2, shaN(2), "success", 6)}, src, 30)
	if r := rowFor(t, rep, 1); r.Local.RunID != "rerun" || r.Verdict != VerdictAgree {
		t.Fatalf("%+v", r)
	}
	r2 := rowFor(t, rep, 2)
	if r2.Verdict != VerdictLocalOnly || !strings.Contains(r2.Note, "could not be read") || len(rep.Warnings) != 1 {
		t.Fatalf("%+v %v", r2, rep.Warnings)
	}
	// entries of another repository or without a gate are left out
	other := localPR(3, shaN(3), "success", 7)
	other.Repo = "x/y"
	nogate := localPR(4, shaN(4), "success", 8)
	nogate.BaseRef = "elsewhere"
	rep = compareOnly(t, []IndexEntry{other, nogate}, src, 30)
	if len(rep.Rows) != 0 {
		t.Fatalf("%+v", rep.Rows)
	}
	if _, err := Compare(context.Background(), CompareOptions{Repo: "o/r"}); err == nil {
		t.Fatal("a missing source is an error")
	}
}

func TestComparePushGateUsesTheBranchTip(t *testing.T) {
	n := StatusNaming{Gates: []GateRule{{Kind: KindPush, Branch: "main", Workflow: "full.yml", Context: "full-gate"}}}
	e := IndexEntry{RunID: "p1", Workflow: "full.yml", Event: "push", Repo: "o/r", HeadRef: "main", HeadSHA: shaN(50), Conclusion: "success", End: cmpT0}
	src := &FakeCompareSource{
		Tips:    map[string]string{"main": shaN(50)},
		Results: map[string]GHChecks{shaN(50): {Runs: []GHCheck{run("full-gate", "completed", "success")}}},
	}
	rep, err := Compare(context.Background(), CompareOptions{Repo: "o/r", Index: []IndexEntry{e}, Source: src, Naming: n})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Kind != KindPush || rep.Rows[0].Verdict != VerdictAgree || rep.Streak != 0 {
		t.Fatalf("push rows agree but do not count toward the pull request streak: %+v", rep)
	}
	// a tip nobody ran locally is github-only
	src.Tips["main"] = shaN(51)
	src.Results[shaN(51)] = GHChecks{Runs: []GHCheck{run("full-gate", "completed", "failure")}}
	rep, _ = Compare(context.Background(), CompareOptions{Repo: "o/r", Index: []IndexEntry{e}, Source: src, Naming: n})
	if rep.GitHubOnly != 1 {
		t.Fatalf("%+v", rep)
	}
	// an unreadable tip is a warning, not a failure
	delete(src.Tips, "main")
	rep, err = Compare(context.Background(), CompareOptions{Repo: "o/r", Source: src, Naming: n})
	if err != nil || len(rep.Warnings) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestCompareJSONRoundTrips(t *testing.T) {
	src := &FakeCompareSource{Results: map[string]GHChecks{shaN(1): {Runs: []GHCheck{run("quick-gate", "completed", "success")}, Statuses: []GHCheck{stat("quick-gate", "success")}}}}
	rep := compareOnly(t, []IndexEntry{localPR(1, shaN(1), "success", 1)}, src, 30)
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back CompareReport
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Streak != 1 || len(back.Rows) != 1 || back.Rows[0].Check == nil || back.Rows[0].Status == nil || back.Rows[0].Verdict != VerdictAgree {
		t.Fatalf("%+v", back)
	}
	for _, k := range []string{`"streak"`, `"gateMet"`, `"verdict"`, `"github"`} {
		if !strings.Contains(buf.String(), k) {
			t.Errorf("json lacks %s", k)
		}
	}
}

// ---------------------------------------------------------------- the gh source

func TestGHCompareSourceParsesWhatGitHubSays(t *testing.T) {
	var asked []string
	g := &GHCompareSource{Env: []string{"GH_TOKEN=x"}, exec: func(_ context.Context, _ []string, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		asked = append(asked, line)
		switch {
		case strings.HasPrefix(line, "pr list"):
			return []byte(`[{"number":7,"baseRefName":"develop","headRefOid":"` + shaN(7) + `","state":"OPEN"}]`), nil
		case strings.Contains(line, "/check-runs"):
			return []byte(`{"check_runs":[{"name":"quick-gate","status":"completed","conclusion":"success","completed_at":"2026-11-20T10:00:00Z"},{"name":"lint","status":"in_progress","conclusion":null}]}`), nil
		case strings.Contains(line, "/statuses"):
			return []byte(`[{"context":"quick-gate","state":"failure","description":"new","created_at":"2026-11-20T11:00:00Z"},{"context":"quick-gate","state":"success","description":"old","created_at":"2026-11-20T09:00:00Z"}]`), nil
		case strings.Contains(line, "/commits/develop"):
			return []byte(shaN(99) + "\n"), nil
		}
		return nil, errors.New("unexpected " + line)
	}}
	ctx := context.Background()
	pulls, err := g.RecentPulls(ctx, "o/r", 5)
	if err != nil || len(pulls) != 1 || pulls[0].SHA != shaN(7) || pulls[0].Base != "develop" {
		t.Fatalf("%v %+v", err, pulls)
	}
	if tip, err := g.BranchTip(ctx, "o/r", "develop"); err != nil || tip != shaN(99) {
		t.Fatalf("%q %v", tip, err)
	}
	c, err := g.Checks(ctx, "o/r", shaN(7))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Runs) != 2 || c.Runs[0].Outcome != OutcomePass || c.Runs[1].Outcome != OutcomePending {
		t.Fatalf("runs %+v", c.Runs)
	}
	if len(c.Statuses) != 1 || c.Statuses[0].State != "failure" || c.Statuses[0].Outcome != OutcomeFail {
		t.Fatalf("only the newest status of a context counts: %+v", c.Statuses)
	}
	if _, err := g.Checks(ctx, "o/r", "main"); err == nil {
		t.Fatal("a branch name is not a commit id")
	}
	for _, a := range asked {
		if strings.Contains(a, "--method") || strings.Contains(a, "POST") || strings.Contains(a, " -f ") {
			t.Errorf("a comparison must only read: %s", a)
		}
	}
}

func TestCheckRunAndStatusOutcomes(t *testing.T) {
	for _, c := range []struct{ status, concl, want string }{
		{"completed", "success", OutcomePass}, {"completed", "skipped", OutcomePass}, {"completed", "neutral", OutcomePass},
		{"completed", "failure", OutcomeFail}, {"completed", "timed_out", OutcomeFail}, {"completed", "cancelled", OutcomeNone},
		{"queued", "", OutcomePending}, {"in_progress", "", OutcomePending}, {"completed", "", OutcomeNone},
	} {
		if got := checkRunOutcome(c.status, c.concl); got != c.want {
			t.Errorf("%s/%s: %s, want %s", c.status, c.concl, got, c.want)
		}
	}
	for state, want := range map[string]string{"success": OutcomePass, "failure": OutcomeFail, "error": OutcomeFail, "pending": OutcomePending, "weird": OutcomeNone} {
		if got := statusOutcome(state); got != want {
			t.Errorf("%s: %s, want %s", state, got, want)
		}
	}
}

// ---------------------------------------------------------------- fallback

type fbHost struct {
	fakeHost
	files map[string]string // by path
	err   error
}

func (f *fbHost) FileAt(_ context.Context, _, _, path string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.files[path]; ok {
		return []byte(s), nil
	}
	return nil, errors.New("not found: " + path)
}

const dispatchWF = "name: q\non:\n  pull_request:\n  workflow_dispatch:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
const noDispatchWF = "name: q\non:\n  pull_request:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"

func fbOptions(pr PullRequest, wf string) (FallbackOptions, *FakeDispatcher) {
	d := &FakeDispatcher{}
	h := &fbHost{fakeHost: fakeHost{pr: pr}, files: map[string]string{".github/workflows/quick.yml": wf}}
	return FallbackOptions{Repo: "o/r", Number: pr.Number, Host: h, Naming: testNaming(false), Dispatcher: d}, d
}

func openPR() PullRequest {
	return PullRequest{Number: 12, State: "OPEN", BaseRef: "develop", HeadRef: "feature/x", HeadSHA: shaN(12), Author: "me", HeadRepoOwner: "o"}
}

func TestFallbackStartsTheGateWorkflowOnTheHeadBranch(t *testing.T) {
	o, d := fbOptions(openPR(), dispatchWF)
	res, err := Fallback(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || res.Workflow != "quick.yml" || res.Branch != "feature/x" || res.Base != "develop" {
		t.Fatalf("%+v", res)
	}
	if len(d.Calls) != 1 || d.Calls[0] != "o/r quick.yml feature/x" {
		t.Fatalf("dispatched %v", d.Calls)
	}
	if res.Command != "gh workflow run quick.yml --repo o/r --ref feature/x" {
		t.Fatalf("command %q", res.Command)
	}
}

func TestFallbackDryRunStartsNothing(t *testing.T) {
	o, d := fbOptions(openPR(), dispatchWF)
	o.DryRun = true
	res, err := Fallback(context.Background(), o)
	if err != nil || res.Started || len(d.Calls) != 0 || res.Workflow != "quick.yml" {
		t.Fatalf("%+v %v %v", res, err, d.Calls)
	}
}

func TestFallbackRefusesWhatItCannotRun(t *testing.T) {
	cases := map[string]func(pr *PullRequest, wf *string, repo *string){
		"closed":        func(pr *PullRequest, _ *string, _ *string) { pr.State = "MERGED" },
		"fork":          func(pr *PullRequest, _ *string, _ *string) { pr.IsFork = true },
		"option branch": func(pr *PullRequest, _ *string, _ *string) { pr.HeadRef = "--ref=evil" },
		"no gate":       func(pr *PullRequest, _ *string, _ *string) { pr.BaseRef = "elsewhere" },
		"no dispatch":   func(_ *PullRequest, wf *string, _ *string) { *wf = noDispatchWF },
		"bad workflow":  func(_ *PullRequest, wf *string, _ *string) { *wf = "on: [" },
		"bad repo":      func(_ *PullRequest, _ *string, repo *string) { *repo = "norepo" },
	}
	for name, mut := range cases {
		pr, wf, repo := openPR(), dispatchWF, "o/r"
		mut(&pr, &wf, &repo)
		o, d := fbOptions(pr, wf)
		o.Repo = repo
		if _, err := Fallback(context.Background(), o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(d.Calls) != 0 {
			t.Errorf("%s: dispatched %v", name, d.Calls)
		}
	}
	// the message says what to add
	o, _ := fbOptions(openPR(), noDispatchWF)
	if _, err := Fallback(context.Background(), o); err == nil || !strings.Contains(err.Error(), "workflow_dispatch") {
		t.Fatalf("%v", err)
	}
	// host and dispatch errors come back, and a failed dispatch is not reported as started
	o, d := fbOptions(openPR(), dispatchWF)
	d.Err = errors.New("HTTP 404")
	res, err := Fallback(context.Background(), o)
	if err == nil || res == nil || res.Started {
		t.Fatalf("%+v %v", res, err)
	}
	o, _ = fbOptions(openPR(), dispatchWF)
	o.Host.(*fbHost).err = errors.New("no such file")
	if _, err := Fallback(context.Background(), o); err == nil {
		t.Fatal("an unreadable workflow must stop the fallback")
	}
	o, _ = fbOptions(openPR(), dispatchWF)
	o.Host.(*fbHost).prErr = errors.New("HTTP 404")
	if _, err := Fallback(context.Background(), o); err == nil {
		t.Fatal("an unreadable pull request must stop the fallback")
	}
	if _, err := Fallback(context.Background(), FallbackOptions{Repo: "o/r"}); err == nil {
		t.Fatal("no host")
	}
}

func TestGHDispatcherRunsOnlyWorkflowRun(t *testing.T) {
	var got []string
	g := &GHDispatcher{Env: []string{"GH_TOKEN=x"}, exec: func(_ context.Context, _ []string, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}}
	if err := g.Dispatch(context.Background(), "o/r", "quick.yml", "feature/x"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "workflow run quick.yml --repo o/r --ref feature/x" {
		t.Fatalf("%v", got)
	}
	if g.Command("o/r", "quick.yml", "feature/x") != "gh workflow run quick.yml --repo o/r --ref feature/x" {
		t.Fatal("command text")
	}
}

// A pull request whose base moved after GitHub ran no longer merges: the local run stops at "merge conflict" and
// there is nothing to compare, so it is not a disagreement and must not break the streak.
func TestCompareMergeConflictIsNotADisagreement(t *testing.T) {
	e := localPR(1, shaN(1), "failure", 1)
	e.Reason = "merge conflict"
	src := &FakeCompareSource{Results: map[string]GHChecks{shaN(1): {Runs: []GHCheck{run("quick-gate", "completed", "success")}}}}
	rep := compareOnly(t, []IndexEntry{e}, src, 30)
	r := rowFor(t, rep, 1)
	if r.Verdict != VerdictLocalOnly || !strings.Contains(r.Note, "rebase") {
		t.Fatalf("verdict %q note %q, want local-only with a note about a rebase", r.Verdict, r.Note)
	}
	if rep.Disagree != 0 {
		t.Fatalf("disagree %d, want 0", rep.Disagree)
	}
}
