package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func needBash(t *testing.T) {
	t.Helper()
	if _, err := findBash(); err != nil {
		t.Skip("bash (Git Bash) not found, skipping:", err)
	}
}

type harness struct {
	t     *testing.T
	opts  RunOptions
	log   *bytes.Buffer
	state string
	work  string
}

// newHarness parses the workflow text and prepares a workdir with a .git folder and a state dir.
func newHarness(t *testing.T, yml string) *harness {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "wf.yml")
	if err := os.WriteFile(file, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	wf, uns, err := ParseWorkflow("wf.yml", []byte(yml))
	if err != nil || len(uns) > 0 {
		t.Fatalf("parse: %v %v", err, uns)
	}
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, log: &bytes.Buffer{}, state: filepath.Join(t.TempDir(), "state"), work: work}
	h.opts = RunOptions{
		Workflow: wf, Workdir: work, StateDir: h.state, Env: os.Environ(), Log: h.log,
		GitHub: map[string]any{"event_name": "push", "ref": "refs/heads/main", "ref_name": "main", "sha": "abc123",
			"repository": "o/r", "run_id": "1", "run_number": "1", "run_attempt": "1", "actor": "me"},
	}
	return h
}

func (h *harness) run() *RunResult {
	h.t.Helper()
	res, err := Run(context.Background(), h.opts)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return res
}

func (h *harness) logText() string { return h.log.String() }

func job(t *testing.T, r *RunResult, key string) *JobResult {
	t.Helper()
	for _, j := range r.Jobs {
		if j.Key == key || j.ID == key && j.Key == key {
			return j
		}
	}
	var keys []string
	for _, j := range r.Jobs {
		keys = append(keys, j.Key)
	}
	t.Fatalf("no job %q in %v", key, keys)
	return nil
}

func stepLog(t *testing.T, s StepResult) string {
	t.Helper()
	b, err := os.ReadFile(s.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatrixNeedsOutputs(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: m
on: push
env:
  WF: wfval
jobs:
  build:
    runs-on: ubuntu-24.04
    strategy:
      matrix:
        os: [ubuntu-24.04]
        n: [1, 2]
    outputs:
      first: ${{ steps.s.outputs.first }}
      who: ${{ matrix.n }}-${{ steps.s.outputs.v }}
    env:
      JOBENV: j${{ matrix.n }}
    steps:
      - id: s
        run: |
          echo "v=$WF-$JOBENV" >> "$GITHUB_OUTPUT"
          echo "first=ok" >> "$GITHUB_OUTPUT"
  use:
    runs-on: ubuntu-24.04
    needs: build
    if: needs.build.result == 'success'
    steps:
      - run: echo "got=${{ needs.build.outputs.first }}/${{ needs.build.outputs.who }}"
  after:
    runs-on: ubuntu-24.04
    needs: [use]
    steps:
      - run: echo after
`)
	res := h.run()
	if res.Conclusion != "success" {
		t.Fatalf("conclusion %s\n%s", res.Conclusion, h.logText())
	}
	if len(res.Jobs) != 4 {
		t.Fatalf("jobs: %d", len(res.Jobs))
	}
	b1 := job(t, res, "build-ubuntu-24.04-1")
	if b1.Label != "build (ubuntu-24.04, 1)" || b1.Matrix["n"] == nil || b1.Outputs["who"] != "1-wfval-j1" {
		t.Fatalf("b1 %+v", b1)
	}
	u := job(t, res, "use")
	if got := stepLog(t, u.Steps[0]); !strings.Contains(got, "got=ok/2-wfval-j2") {
		t.Fatalf("use log %q (outputs come from the last instance)", got)
	}
	if job(t, res, "after").Status != "success" {
		t.Fatal("after")
	}
	if !strings.Contains(h.logText(), "step 1/1 Run echo after ... ok") {
		t.Fatalf("log:\n%s", h.logText())
	}
}

func TestStatusFunctionsAndContinueOnError(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: s
on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    steps:
      - id: coe
        run: exit 3
        continue-on-error: true
      - id: after_coe
        run: echo runs-after-coe
      - id: bad
        run: exit 1
      - name: skipped by failure
        run: echo should-not-run
      - name: cleanup
        if: always()
        run: echo cleanup
      - name: on failure
        if: failure() && steps.bad.outcome == 'failure' && steps.coe.conclusion == 'success' && steps.coe.outcome == 'failure'
        run: echo reported
      - name: only success
        if: success()
        run: echo no
  b:
    runs-on: ubuntu-24.04
    needs: a
    steps:
      - run: echo b-should-be-skipped
  c:
    runs-on: ubuntu-24.04
    needs: a
    if: failure()
    steps:
      - run: echo c-runs
  d:
    runs-on: ubuntu-24.04
    needs: a
    if: always()
    steps:
      - run: echo "a=${{ needs.a.result }}"
  e:
    runs-on: ubuntu-24.04
    needs: b
    if: ${{ needs.b.result == 'skipped' || always() }}
    steps:
      - run: echo e-runs
`)
	res := h.run()
	if res.Conclusion != "failure" {
		t.Fatalf("conclusion %s", res.Conclusion)
	}
	a := job(t, res, "a")
	want := []string{"success", "success", "failure", "skipped", "success", "success", "skipped"}
	for i, s := range a.Steps {
		if s.Conclusion != want[i] {
			t.Errorf("step %d %q conclusion %s want %s (%s)", i+1, s.Name, s.Conclusion, want[i], s.Err)
		}
	}
	if a.Steps[0].Outcome != "failure" || a.Steps[0].Conclusion != "success" {
		t.Errorf("continue-on-error step: %+v", a.Steps[0])
	}
	if a.Steps[3].Outcome != "skipped" {
		t.Errorf("skipped outcome %q", a.Steps[3].Outcome)
	}
	if a.Status != "failure" {
		t.Errorf("a %s", a.Status)
	}
	if job(t, res, "b").Status != "skipped" {
		t.Errorf("b %s", job(t, res, "b").Status)
	}
	if job(t, res, "c").Status != "success" || job(t, res, "d").Status != "success" || job(t, res, "e").Status != "success" {
		t.Errorf("c/d/e: %s %s %s", job(t, res, "c").Status, job(t, res, "d").Status, job(t, res, "e").Status)
	}
	if got := stepLog(t, job(t, res, "d").Steps[0]); !strings.Contains(got, "a=failure") {
		t.Errorf("d log %q", got)
	}
}

func TestJobContinueOnError(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: c
on: push
jobs:
  soft:
    runs-on: ubuntu-24.04
    continue-on-error: true
    steps:
      - run: exit 1
  next:
    runs-on: ubuntu-24.04
    needs: soft
    steps:
      - run: echo "r=${{ needs.soft.result }}"
`)
	res := h.run()
	if job(t, res, "next").Status != "success" {
		t.Fatalf("next %s", job(t, res, "next").Status)
	}
	if res.Conclusion != "success" {
		t.Fatalf("a continue-on-error job must not fail the run, got %s", res.Conclusion)
	}
}

func TestTimeoutKillsProcessTree(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: t
on: push
jobs:
  t:
    runs-on: ubuntu-24.04
    timeout-minutes: 5
    steps:
      - run: |
          sleep 30 &
          sleep 30
      - if: always()
        run: echo cleanup-ran
`)
	h.opts.stepTimeoutOverride = time.Second
	start := time.Now()
	res := h.run()
	if el := time.Since(start); el > 2500*time.Millisecond+time.Second {
		t.Fatalf("the process tree was not killed promptly: %s", el)
	}
	j := job(t, res, "t")
	if j.Status != "failure" || j.Steps[0].Conclusion != "failure" || !strings.HasPrefix(j.Steps[0].Err, "timed out after") {
		t.Fatalf("job %+v", j)
	}
	if j.Steps[1].Conclusion != "success" {
		t.Fatalf("always() step: %+v", j.Steps[1])
	}
}

func TestSpecialFiles(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: f
on: push
jobs:
  f:
    runs-on: ubuntu-24.04
    steps:
      - id: one
        run: |
          echo "plain=value=with=equals" >> "$GITHUB_OUTPUT"
          {
            echo "multi<<EOT"
            echo "line one"
            echo "line two"
            echo "EOT"
          } >> "$GITHUB_OUTPUT"
          echo "FROM_ENV=hello" >> "$GITHUB_ENV"
          {
            echo "BLOCK<<DELIM"
            echo "a"
            echo "b"
            echo "DELIM"
          } >> "$GITHUB_ENV"
          mkdir -p "$RUNNER_TEMP/bin"
          printf '#!/bin/sh\necho from-path-tool\n' > "$RUNNER_TEMP/bin/mytool"
          chmod +x "$RUNNER_TEMP/bin/mytool"
          echo "$RUNNER_TEMP/bin" >> "$GITHUB_PATH"
          echo "### Summary line" >> "$GITHUB_STEP_SUMMARY"
      - id: two
        env:
          STEP_ENV: ${{ steps.one.outputs.plain }}
        run: |
          echo "env=$FROM_ENV block=$BLOCK step=$STEP_ENV"
          mytool
          echo "multi=${{ steps.one.outputs.multi }}"
          echo "os=${{ runner.os }} $RUNNER_OS ci=$CI sha=$GITHUB_SHA ws=$GITHUB_WORKSPACE tok=[${GITHUB_TOKEN-unset}]"
      - id: three
        run: echo "env=${{ env.FROM_ENV }}"
`)
	h.opts.Env = append(os.Environ(), "GITHUB_TOKEN=leak")
	res := h.run()
	j := job(t, res, "f")
	if j.Status != "success" {
		t.Fatalf("job %+v\n%s", j, h.logText())
	}
	two := stepLog(t, j.Steps[1])
	for _, want := range []string{"env=hello block=a\nb step=value=with=equals", "from-path-tool", "os=Linux Linux ci=true sha=abc123", "tok=[unset]"} {
		if !strings.Contains(two, want) {
			t.Errorf("step two log lacks %q:\n%s", want, two)
		}
	}
	if !strings.Contains(two, "multi=line one\nline two") {
		t.Errorf("multi-line output:\n%s", two)
	}
	if !strings.Contains(stepLog(t, j.Steps[2]), "env=hello") {
		t.Errorf("env context:\n%s", stepLog(t, j.Steps[2]))
	}
	sum, _ := os.ReadFile(filepath.Join(h.state, "summary.md"))
	if !strings.Contains(string(sum), "## f\n") || !strings.Contains(string(sum), "### Summary line") {
		t.Errorf("summary %q", sum)
	}
}

func TestMalformedOutputFileFailsStep(t *testing.T) {
	needBash(t)
	h := newHarness(t, "name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo garbage >> \"$GITHUB_OUTPUT\"\n")
	res := h.run()
	if j := job(t, res, "j"); j.Status != "failure" || !strings.Contains(j.Steps[0].Err, "GITHUB_OUTPUT") {
		t.Fatalf("%+v", j)
	}
}

func TestAddMaskAndRedact(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: x
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - run: |
          echo "::add-mask::hunter2"
          echo "password is hunter2"
          echo "::warning::keep hunter2 hidden"
          echo "::set-output name=a::b"
          echo "redact-me here"
      - run: echo "later hunter2 still masked"
`)
	h.opts.Redact = func(s string) string { return strings.ReplaceAll(s, "redact-me", "[R]") }
	res := h.run()
	j := job(t, res, "j")
	l1, l2 := stepLog(t, j.Steps[0]), stepLog(t, j.Steps[1])
	if strings.Contains(l1, "hunter2") || strings.Contains(l2, "hunter2") {
		t.Fatalf("secret leaked:\n%s\n%s", l1, l2)
	}
	for _, want := range []string{"password is ***", "::warning::keep *** hidden", "::set-output name=a::b", "[R] here"} {
		if !strings.Contains(l1, want) {
			t.Errorf("log lacks %q:\n%s", want, l1)
		}
	}
	if strings.Contains(l1, "::add-mask::") {
		t.Errorf("the add-mask command should not be printed:\n%s", l1)
	}
	if !strings.Contains(l2, "later *** still masked") {
		t.Errorf("l2 %q", l2)
	}
}

func TestDefaultsShellAndWorkingDirectory(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: d
on: push
defaults:
  run:
    working-directory: sub
jobs:
  bashjob:
    runs-on: ubuntu-24.04
    steps:
      - run: |
          pwd
          false | true
          echo not-reached-under-pipefail
  shjob:
    runs-on: ubuntu-24.04
    defaults:
      run:
        shell: sh
    steps:
      - run: |
          false | true
          echo reached-in-sh
      - working-directory: .
        run: ls sub
`)
	if err := os.MkdirAll(filepath.Join(h.work, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	res := h.run()
	b := job(t, res, "bashjob")
	if b.Status != "failure" || strings.Contains(stepLog(t, b.Steps[0]), "not-reached") || !strings.Contains(stepLog(t, b.Steps[0]), "/sub") {
		t.Fatalf("bash default must be pipefail in the working dir: %+v\n%s", b, stepLog(t, b.Steps[0]))
	}
	s := job(t, res, "shjob")
	if s.Status != "success" || !strings.Contains(stepLog(t, s.Steps[0]), "reached-in-sh") {
		t.Fatalf("sh job: %+v", s)
	}
}

func TestNotRunLocally(t *testing.T) {
	needBash(t)
	cfg, err := ParseLocalConfig([]byte(`
runs-on:
  macos-latest: skip
jobs:
  wf.yml:
    mac-accepted:
      skip: needs a Mac
      accepted: true
`))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, `
name: n
on: push
jobs:
  mac-accepted:
    runs-on: ubuntu-24.04
    steps:
      - run: echo never
  mac-label:
    runs-on: macos-latest
    steps:
      - run: echo never
  after-accepted:
    runs-on: ubuntu-24.04
    needs: mac-accepted
    steps:
      - run: echo "r=${{ needs.mac-accepted.result }}"
  after-label:
    runs-on: ubuntu-24.04
    needs: mac-label
    steps:
      - run: echo never
  observer:
    runs-on: ubuntu-24.04
    needs: mac-label
    if: always()
    steps:
      - run: echo "r=${{ needs.mac-label.result }}"
`)
	h.opts.Config = cfg
	res := h.run()
	ma := job(t, res, "mac-accepted")
	if ma.Status != "not_run_locally" || ma.Reason != "needs a Mac" {
		t.Fatalf("%+v", ma)
	}
	if got := job(t, res, "mac-label"); got.Status != "not_run_locally" || !strings.Contains(got.Reason, "macos-latest") {
		t.Fatalf("%+v", got)
	}
	aa := job(t, res, "after-accepted")
	if aa.Status != "success" || !strings.Contains(stepLog(t, aa.Steps[0]), "r=success") {
		t.Fatalf("after-accepted %+v", aa)
	}
	if job(t, res, "after-label").Status != "skipped" {
		t.Fatalf("after-label %s", job(t, res, "after-label").Status)
	}
	if got := stepLog(t, job(t, res, "observer").Steps[0]); !strings.Contains(got, "r=failure") {
		t.Fatalf("observer %q", got)
	}
	if res.Conclusion != "failure" {
		t.Fatalf("an unaccepted not_run_locally job must not let the run pass, got %s", res.Conclusion)
	}
}

func TestFailFastCancelsSiblings(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: ff
on: push
jobs:
  m:
    runs-on: ubuntu-24.04
    strategy:
      matrix:
        which: [fail, slow]
    steps:
      - run: |
          if [ "${{ matrix.which }}" = fail ]; then sleep 0.5; exit 1; fi
          sleep 30
`)
	start := time.Now()
	res := h.run()
	if time.Since(start) > 15*time.Second {
		t.Fatal("the slow sibling was not cancelled")
	}
	if got := job(t, res, "m-fail").Status; got != "failure" {
		t.Fatalf("fail: %s", got)
	}
	slow := job(t, res, "m-slow")
	if slow.Status != "cancelled" || slow.Steps[0].Conclusion != "cancelled" {
		t.Fatalf("slow: %+v", slow)
	}
}

func TestFailFastFalseKeepsSiblings(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: ff
on: push
jobs:
  m:
    runs-on: ubuntu-24.04
    strategy:
      fail-fast: false
      matrix:
        which: [fail, ok]
    steps:
      - run: |
          if [ "${{ matrix.which }}" = fail ]; then exit 1; fi
          sleep 1
`)
	res := h.run()
	if job(t, res, "m-fail").Status != "failure" || job(t, res, "m-ok").Status != "success" {
		t.Fatalf("%s %s", job(t, res, "m-fail").Status, job(t, res, "m-ok").Status)
	}
}

func TestOnlyAndSlots(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: o
on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    steps:
      - run: echo a
  b:
    runs-on: ubuntu-24.04
    needs: a
    steps:
      - run: echo b
  c:
    runs-on: ubuntu-24.04
    steps:
      - run: echo c
`)
	h.opts.Only = []string{"b"}
	res := h.run()
	if len(res.Jobs) != 1 || res.Jobs[0].ID != "b" || res.Jobs[0].Status != "success" {
		t.Fatalf("%+v", res.Jobs)
	}
	h.opts.Only = []string{"nope"}
	if _, err := Run(context.Background(), h.opts); err == nil || !strings.Contains(err.Error(), "no such job") {
		t.Fatalf("got %v", err)
	}
}

func TestSlotsLimitConcurrency(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: s
on: push
jobs:
  m:
    runs-on: ubuntu-24.04
    strategy:
      matrix:
        i: [1, 2, 3, 4]
    steps:
      - run: sleep 0.4
`)
	h.opts.Slots = 1
	start := time.Now()
	res := h.run()
	if res.Conclusion != "success" {
		t.Fatal(res.Conclusion)
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatalf("four 0.4s jobs on one slot finished in %s", time.Since(start))
	}
}

func TestPreflightRefusesUnsupported(t *testing.T) {
	wf, uns, err := ParseWorkflow("bad.yml", []byte("name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: docker/build-push-action@v5\n      - uses: other/thing@v1\n"))
	if err != nil || len(uns) != 2 {
		t.Fatalf("%v %v", err, uns)
	}
	_, err = Run(context.Background(), RunOptions{Workflow: wf, Workdir: t.TempDir(), StateDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "unsupported: uses docker/build-push-action") || !strings.Contains(err.Error(), "unsupported: uses other/thing") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSetupErrors(t *testing.T) {
	if _, err := Run(context.Background(), RunOptions{}); err == nil {
		t.Fatal("want error without a workflow")
	}
	h := newHarness(t, "name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo\n")
	h.opts.Workdir = filepath.Join(t.TempDir(), "missing")
	if _, err := Run(context.Background(), h.opts); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("got %v", err)
	}
}

func TestShimStepsAndCheckoutFailure(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: sh
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 2
      - id: c
        uses: actions/cache/restore@v4
        with:
          path: x
          key: k-${{ github.sha }}
      - run: echo "hit=${{ steps.c.outputs.cache-hit }}"
      - run: echo data > out.txt
      - uses: actions/upload-artifact@v4
        with:
          name: o
          path: out.txt
`)
	res := h.run()
	j := job(t, res, "j")
	if j.Status != "success" {
		t.Fatalf("%+v\n%s", j, h.logText())
	}
	if !strings.Contains(stepLog(t, j.Steps[2]), "hit=false") {
		t.Fatal("cache-hit output")
	}
	if _, err := os.Stat(filepath.Join(h.state, "artifacts", "o", "out.txt")); err != nil {
		t.Fatal(err)
	}
	// checkout in a folder without .git fails the step
	h2 := newHarness(t, "name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/checkout@v4\n")
	if err := os.RemoveAll(filepath.Join(h2.work, ".git")); err != nil {
		t.Fatal(err)
	}
	j2 := job(t, h2.run(), "j")
	if j2.Status != "failure" || !strings.Contains(j2.Steps[0].Err, ".git") {
		t.Fatalf("%+v", j2)
	}
}

func TestIfExpressionErrorFailsStep(t *testing.T) {
	needBash(t)
	h := newHarness(t, "name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo hi\n        env:\n          A: ${{ fromJSON('{') }}\n")
	j := job(t, h.run(), "j")
	if j.Status != "failure" || j.Steps[0].Err == "" {
		t.Fatalf("%+v", j)
	}
}

func TestInputsAndVars(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: i
on:
  workflow_dispatch:
    inputs:
      who:
        default: world
      loud:
        type: boolean
        default: false
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - run: echo "hello ${{ inputs.who }} v=${{ vars.V }} s=[${{ secrets.S }}] t=[${{ github.token }}]"
      - if: ${{ inputs.loud }}
        run: echo LOUD
`)
	h.opts.Inputs = map[string]string{"who": "there", "loud": "true"}
	h.opts.Vars = map[string]string{"V": "vv"}
	h.opts.GitHub["token"] = "should-be-blanked"
	j := job(t, h.run(), "j")
	if got := stepLog(t, j.Steps[0]); !strings.Contains(got, "hello there v=vv s=[] t=[]") {
		t.Fatalf("%q", got)
	}
	if j.Steps[1].Conclusion != "success" {
		t.Fatalf("%+v", j.Steps[1])
	}
}

func TestWriteRunRecord(t *testing.T) {
	r := &RunResult{Workflow: "w", Conclusion: "success", Jobs: []*JobResult{{Key: "a", ID: "a", Label: "a", Status: "success", Steps: []StepResult{{Index: 1, Name: "n", Outcome: "success", Conclusion: "success"}}}}}
	dir := filepath.Join(t.TempDir(), "state")
	if err := WriteRunRecord(dir, r); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "run.json")) //nolint:gosec // path built by the tool/test itself, reading it is the point
	if err != nil {
		t.Fatal(err)
	}
	var back RunResult
	if err := json.Unmarshal(b, &back); err != nil || back.Jobs[0].Steps[0].Name != "n" || back.Conclusion != "success" {
		t.Fatalf("%v %+v", err, back)
	}
}

func TestDryRunFastGatePlan(t *testing.T) {
	file := filepath.Join("..", "..", ".github", "workflows", "fast-gate.yml")
	data, err := os.ReadFile(file) //nolint:gosec // path built by the tool/test itself, reading it is the point
	if err != nil {
		t.Skip("fast-gate.yml not found:", err)
	}
	wf, uns, err := ParseWorkflow("fast-gate.yml", data)
	if err != nil || len(uns) > 0 {
		t.Fatalf("parse: %v %v", err, uns)
	}
	var log bytes.Buffer
	res, err := Run(context.Background(), RunOptions{Workflow: wf, Workdir: t.TempDir(), Log: &log, DryRun: true,
		GitHub: map[string]any{"event_name": "pull_request"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, j := range res.Jobs {
		ids[j.ID] = true
		if j.Status != "planned" {
			t.Errorf("%s status %s", j.ID, j.Status)
		}
	}
	for _, want := range []string{"go", "web", "lint", "fast-build"} {
		if !ids[want] {
			t.Errorf("plan lacks job %s: %v", want, ids)
		}
	}
	out := log.String()
	for _, want := range []string{"job go:", "uses actions/checkout", "uses actions/setup-go", "uses actions/setup-node", "uses actions/cache/restore", "run bash", "needs go,web"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Set up Go") {
		t.Errorf("step names missing:\n%s", out)
	}
}

func TestDryRunDoesNotCreateState(t *testing.T) {
	h := newHarness(t, "name: x\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    strategy:\n      matrix:\n        a: [1, 2]\n    steps:\n      - run: exit 1\n")
	h.opts.DryRun = true
	res := h.run()
	if len(res.Jobs) != 2 || res.Conclusion != "success" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(h.state); err == nil {
		t.Fatal("dry run must not create the state dir")
	}
}

func TestStepExitZeroWithBackgroundProcessSucceedsFast(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: bg
on: push
jobs:
  ok:
    runs-on: ubuntu-24.04
    steps:
      - run: |
          sleep 30 &
          echo done
  bad:
    runs-on: ubuntu-24.04
    steps:
      - run: |
          sleep 30 &
          exit 3
`)
	h.opts.waitDelayOverride = 500 * time.Millisecond
	start := time.Now()
	res := h.run()
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("took %v: the background process held the step", d)
	}
	if j := job(t, res, "ok"); j.Status != "success" || !strings.Contains(stepLog(t, j.Steps[0]), "done") {
		t.Fatalf("exit 0 with a background process must succeed: %+v", j)
	}
	if j := job(t, res, "bad"); j.Status != "failure" || !strings.Contains(j.Steps[0].Err, "exit code 3") {
		t.Fatalf("a real failure must stay a failure: %+v", j)
	}
}

func TestJobContinueOnErrorIsEvaluatedPerInstance(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: coe
on: push
jobs:
  tolerant:
    runs-on: ubuntu-24.04
    continue-on-error: ${{ matrix.exp }}
    strategy:
      matrix:
        exp: [true]
    steps:
      - run: exit 1
  strict:
    runs-on: ubuntu-24.04
    continue-on-error: ${{ matrix.exp }}
    strategy:
      matrix:
        exp: [false]
    steps:
      - run: exit 1
  next:
    runs-on: ubuntu-24.04
    needs: tolerant
    steps:
      - run: echo after
`)
	res := h.run()
	if j := job(t, res, "tolerant-true"); j.Status != "failure" || !j.coe {
		t.Fatalf("tolerant instance: %+v", j)
	}
	if j := job(t, res, "next"); j.Status != "success" {
		t.Fatalf("a continue-on-error failure must not block needs: %+v", j)
	}
	if j := job(t, res, "strict-false"); j.coe {
		t.Fatalf("matrix.exp=false must not tolerate the failure")
	}
	if res.Conclusion != "failure" {
		t.Fatalf("the strict job fails the run, got %s", res.Conclusion)
	}
}

func TestJobContinueOnErrorOnlyJobSucceeds(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: coe
on: push
jobs:
  tolerant:
    runs-on: ubuntu-24.04
    continue-on-error: ${{ matrix.exp }}
    strategy:
      matrix:
        exp: [true]
    steps:
      - run: exit 1
`)
	if res := h.run(); res.Conclusion != "success" {
		t.Fatalf("conclusion %s: %+v", res.Conclusion, res.Jobs[0])
	}
}

func TestJobContinueOnErrorExpressionErrorIsLoud(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: coe
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    continue-on-error: ${{ fromJSON('{') }}
    steps:
      - run: echo hi
`)
	res := h.run()
	j := job(t, res, "j")
	if j.Status != "failure" || !strings.Contains(j.Reason, "continue-on-error") {
		t.Fatalf("an unevaluable continue-on-error must fail the job with a clear reason: %+v", j)
	}
	if res.Conclusion != "failure" {
		t.Fatalf("conclusion %s", res.Conclusion)
	}
}

func TestJobTimeoutIsOneDeadlineForTheWholeJob(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: jt
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    timeout-minutes: 5
    steps:
      - run: sleep 1
      - run: sleep 1
      - run: sleep 1
      - run: echo never
`)
	h.opts.jobTimeoutOverride = 2500 * time.Millisecond
	start := time.Now()
	res := h.run()
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("took %v", d)
	}
	j := job(t, res, "j")
	if j.Status != "failure" {
		t.Fatalf("the job must fail at its deadline: %+v", j)
	}
	limited := 0
	for _, s := range j.Steps {
		if strings.Contains(s.Err, "job limit") || strings.Contains(s.Err, "job timed out") {
			limited++
		}
	}
	if limited != 1 {
		t.Fatalf("exactly one step hits the job deadline: %+v", j.Steps)
	}
	if last := j.Steps[len(j.Steps)-1]; last.Conclusion == "success" {
		t.Fatalf("no step may run to success after the deadline: %+v", last)
	}
}

func TestJobOutputsPublishedWhenJobFails(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: out
on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    outputs:
      v: ${{ steps.s.outputs.v }}
    steps:
      - id: s
        run: |
          echo "v=hello" >> "$GITHUB_OUTPUT"
          exit 1
  b:
    runs-on: ubuntu-24.04
    needs: a
    if: always()
    steps:
      - run: echo "got=${{ needs.a.outputs.v }}"
`)
	res := h.run()
	a := job(t, res, "a")
	if a.Status != "failure" || a.Outputs["v"] != "hello" {
		t.Fatalf("outputs must be published whatever the status: %+v", a)
	}
	b := job(t, res, "b")
	if b.Status != "success" || !strings.Contains(stepLog(t, b.Steps[0]), "got=hello") {
		t.Fatalf("an always() job must read needs.a.outputs: %+v", b)
	}
}

func TestRedactMasksEverySink(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: r
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    outputs:
      o: ${{ steps.s.outputs.v }}
    steps:
      - id: s
        run: |
          v=top; v="${v}secretvalue"
          echo "out $v"
          echo "err $v" >&2
          echo "::warning::w $v"
          echo "v=$v" >> "$GITHUB_OUTPUT"
          echo "sum $v" >> "$GITHUB_STEP_SUMMARY"
      - run: echo clean
`)
	h.opts.Redact = func(s string) string { return strings.ReplaceAll(s, "topsecretvalue", "***") }
	res := h.run()
	if j := job(t, res, "j"); j.Status != "success" || j.Outputs["o"] != "***" {
		t.Fatalf("job outputs must be masked: %+v", j)
	}
	if strings.Contains(h.logText(), "topsecretvalue") {
		t.Fatalf("console leaked:\n%s", h.logText())
	}
	err := filepath.WalkDir(h.state, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // reading the test's own state folder
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "topsecretvalue") {
			t.Errorf("%s leaked the secret:\n%s", p, b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.state, "summary.md")); !strings.Contains(string(b), "sum ***") {
		t.Fatalf("summary %q", b)
	}
	if err := WriteRunRecord(h.state, res); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.state, "run.json")); strings.Contains(string(b), "topsecretvalue") {
		t.Fatalf("run.json leaked: %s", b)
	}
}

func TestStepsKeepOnlyThePlaceholderToken(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: f
on: push
jobs:
  f:
    runs-on: ubuntu-24.04
    steps:
      - run: echo "gh=[$GH_TOKEN] github=[$GITHUB_TOKEN] other=[$MY_TOKEN]"
        env:
          MY_TOKEN: from-the-workflow
`)
	h.opts.Env = append(os.Environ(), "GH_TOKEN="+NoTokenPlaceholder, "GITHUB_TOKEN=leak")
	res := h.run()
	j := job(t, res, "f")
	if j.Status != "success" {
		t.Fatalf("job %+v\n%s", j, h.logText())
	}
	if got := stepLog(t, j.Steps[0]); !strings.Contains(got, "gh=["+NoTokenPlaceholder+"] github=[] other=[from-the-workflow]") {
		t.Fatalf("only the placeholder survives the token strip:\n%s", got)
	}
}

// Two jobs of one workflow must not share a checkout: one job's wipe of a folder (npm ci) cannot reach the other.
func TestJobTreeGivesEachJobItsOwnCheckout(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: s
on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    steps:
      - run: pwd > "$GITHUB_WORKSPACE/who-a.txt"; echo "$GITHUB_WORKSPACE" > "$RUNNER_TEMP/ws-a"
  b:
    runs-on: ubuntu-24.04
    steps:
      - run: test ! -e who-a.txt && echo "$GITHUB_WORKSPACE" > "$RUNNER_TEMP/ws-b"
`)
	var made []string
	var mu sync.Mutex
	h.opts.JobTree = func(_ context.Context, name string) (string, func(), error) {
		d := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(d, 0o750); err != nil {
			return "", nil, err
		}
		mu.Lock()
		made = append(made, d)
		mu.Unlock()
		return d, func() { _ = os.RemoveAll(d) }, nil
	}
	res := h.run()
	if res.Conclusion != "success" {
		t.Fatalf("%s\n%s", res.Conclusion, h.log.String())
	}
	if len(made) != 2 {
		t.Fatalf("want a tree per job, made %v", made)
	}
	if _, err := os.Stat(filepath.Join(h.work, "who-a.txt")); err == nil {
		t.Fatal("job a wrote into the shared checkout")
	}
}

func TestSingleJobKeepsTheRunCheckout(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: s
on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    steps:
      - run: echo hi > in-run-checkout.txt
`)
	h.opts.JobTree = func(context.Context, string) (string, func(), error) {
		t.Error("a one-job workflow needs no extra tree")
		return "", nil, nil
	}
	if res := h.run(); res.Conclusion != "success" {
		t.Fatal(res.Conclusion)
	}
	if _, err := os.Stat(filepath.Join(h.work, "in-run-checkout.txt")); err != nil {
		t.Fatal(err)
	}
}
