package localci

import (
	"strings"
	"testing"
)

func watchRun(t *testing.T, watcher string) (*RunResult, *harness) {
	t.Helper()
	needBash(t)
	h := newHarness(t, `
name: m
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - name: tests
        run: echo tests-ran
      - name: after
        run: echo after-ran
`)
	cfg, err := ParseLocalConfig([]byte("jobs:\n  wf.yml:\n    j:\n      step-watch:\n        tests: |\n" + indent(watcher)))
	if err != nil {
		t.Fatal(err)
	}
	h.opts.Config = cfg
	return h.run(), h
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		b.WriteString("          " + l + "\n")
	}
	return b.String()
}

const quietWatcher = `touch {stop}.ready
while [ ! -e {stop} ]; do sleep 0.1; done`

func TestStepWatchFailsAStepWhenTheWatcherLoggedSomething(t *testing.T) {
	res, h := watchRun(t, quietWatcher+"\necho '4242|x.test.exe|#32770|A test box' >> {log}")
	if got := job(t, res, "j").Status; got != "failure" {
		t.Fatalf("job %s, want failure:\n%s", got, h.logText())
	}
	if !strings.Contains(h.logText(), "step 1/2 tests ... FAILED") || !strings.Contains(h.logText(), "the step watcher saw 1 problem") {
		t.Fatalf("the failure should name the watcher:\n%s", h.logText())
	}
	if !strings.Contains(stepLog(t, job(t, res, "j").Steps[0]), "watcher: 4242|x.test.exe|#32770|A test box") {
		t.Fatalf("the step log should carry the watcher's line:\n%s", stepLog(t, job(t, res, "j").Steps[0]))
	}
}

func TestStepWatchQuietWatcherLeavesTheStepGreen(t *testing.T) {
	res, h := watchRun(t, quietWatcher)
	if got := job(t, res, "j").Status; got != "success" {
		t.Fatalf("job %s, want success:\n%s", got, h.logText())
	}
}

func TestStepWatchThatCannotStartOnlyWarns(t *testing.T) {
	res, h := watchRun(t, "exit 3")
	if got := job(t, res, "j").Status; got != "success" {
		t.Fatalf("job %s, want success (a broken watcher must not turn a good run red):\n%s", got, h.logText())
	}
	if !strings.Contains(stepLog(t, job(t, res, "j").Steps[0]), "the step watcher exited before it was ready") {
		t.Fatalf("the step log should say it was not watched:\n%s", stepLog(t, job(t, res, "j").Steps[0]))
	}
}
