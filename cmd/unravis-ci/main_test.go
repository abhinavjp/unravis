package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cli(t *testing.T, env []string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var so, se bytes.Buffer
	code = realMain(context.Background(), args, &so, &se, env)
	return code, so.String(), se.String()
}

func TestScrubEnv(t *testing.T) {
	dropped := []string{
		"GH_TOKEN=x", "GITHUB_TOKEN=x", "gh_token=x", "NPM_TOKEN=x", "my_secret=x", "DB_PASSWORD=x", "SIGNING_PASSPHRASE=x",
		"GPG_PASSPHRASE_FILE=x", "PROTON_BRIDGE_USER=x", "ProtonPass=x", "SIGNPATH_API_TOKEN=x", "SIGNPATH_ORG=x",
		"UNRAVIS_RELEASE_KEY=x", "unravis_release=x", "AWS_SESSION_TOKEN=x",
	}
	kept := []string{
		"PATH=/usr/bin", "SystemRoot=C:\\Windows", "TEMP=C:\\t", "USERPROFILE=C:\\u", "GOPATH=/g", "GOCACHE=/c", "GOMODCACHE=/m",
		"HOME=/h", "TOKENIZER=1", "SECRETARY=1", "UNRAVIS_HOME=1", "=C:=C:\\", "GITHUB_WORKSPACE=/w", "LOCALAPPDATA=C:\\l",
	}
	got := scrubEnv(append(append([]string{}, dropped...), kept...))
	if !reflect.DeepEqual(got, kept) {
		t.Fatalf("scrubEnv\n got %v\nwant %v", got, kept)
	}
}

func TestVersionAndUsage(t *testing.T) {
	if code, out, _ := cli(t, nil, "version"); code != 0 || !strings.Contains(out, "unravis-ci") {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, _ := cli(t, nil); code != exitUsage {
		t.Fatalf("no args: %d", code)
	}
	if code, _, _ := cli(t, nil, "frobnicate"); code != exitUsage {
		t.Fatalf("unknown: %d", code)
	}
	if code, _, e := cli(t, nil, "run", "x.yml", "--event", "bogus"); code != exitUsage || !strings.Contains(e, "--event") {
		t.Fatalf("bad event: %d %q", code, e)
	}
	if code, _, _ := cli(t, nil, "run", "--event", "push"); code != exitUsage {
		t.Fatalf("no workflow: %d", code)
	}
	if code, _, _ := cli(t, nil, "run", "x.yml", "--event", "push", "--input", "novalue"); code != exitUsage {
		t.Fatalf("bad input: %d", code)
	}
}

func realWorkflows() string { return filepath.Join("..", "..", ".github", "workflows") }

func TestLintRealWorkflows(t *testing.T) {
	if _, err := os.Stat(realWorkflows()); err != nil {
		t.Skip("repository workflows not found:", err)
	}
	code, out, _ := cli(t, nil, "lint", "--workflows", realWorkflows())
	if code != exitUnsupported {
		t.Fatalf("without --allow want exit 3, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "fast-gate.yml: ok") || !strings.Contains(out, "unsupported: ") || !strings.Contains(out, "release.yml") {
		t.Fatalf("output:\n%s", out)
	}
	code, out, errOut := cli(t, nil, "lint", "--workflows", realWorkflows(), "--allow", "release.yml,verify-release.yml")
	if code != exitOK {
		t.Fatalf("with --allow want exit 0, got %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "release.yml: kept on GitHub") || !strings.Contains(out, "verify-release.yml: kept on GitHub") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestLintErrors(t *testing.T) {
	if code, _, _ := cli(t, nil, "lint", "--workflows", filepath.Join(t.TempDir(), "nope")); code != exitUsage {
		t.Fatalf("missing dir: %d", code)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.yml"), []byte("name: a\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := cli(t, nil, "lint", "--workflows", dir); code != exitOK || !strings.Contains(out, "a.yml: ok") {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, e := cli(t, nil, "lint", "--workflows", dir, "--allow", "ghost.yml"); code != exitUsage || !strings.Contains(e, "ghost.yml") {
		t.Fatalf("allow of a missing file: %d %q", code, e)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yml"), []byte("name: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := cli(t, nil, "lint", "--workflows", dir); code != exitUsage {
		t.Fatalf("structural error: %d", code)
	}
}

func needBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		if _, serr := os.Stat(`C:\Program Files\Git\bin\bash.exe`); serr != nil {
			t.Skip("bash not found, skipping:", err)
		}
	}
}

func writeWF(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func checkout(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRunPassFailAndFlagsAfterPositional(t *testing.T) {
	needBash(t)
	env := os.Environ()
	work := checkout(t)
	pass := writeWF(t, "p.yml", "name: p\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo \"ref=$GITHUB_REF sha=$GITHUB_SHA ev=$GITHUB_EVENT_NAME in=${{ inputs.k }} v=${{ vars.q }} tok=[${NPM_TOKEN-unset}]\"\n")
	state := filepath.Join(t.TempDir(), "st")
	code, out, errOut := cli(t, append(env, "NPM_TOKEN=leak"), "run", pass, "--event", "push", "--local", work, "--state", state,
		"--ref", "refs/heads/x", "--sha", "deadbeef", "--var", "q=qq")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"result: success", "step 1/1", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	logs, _ := filepath.Glob(filepath.Join(state, "logs", "j", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(b), "ref=refs/heads/x sha=deadbeef ev=push") || !strings.Contains(string(b), "v=qq") || !strings.Contains(string(b), "tok=[unset]") {
		t.Fatalf("log %q", b)
	}
	if _, err := os.Stat(filepath.Join(state, "run.json")); err != nil {
		t.Fatal(err)
	}

	fail := writeWF(t, "f.yml", "name: f\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: exit 7\n")
	code, out, _ = cli(t, env, "run", fail, "--event", "push", "--local", work, "--state", filepath.Join(t.TempDir(), "s2"))
	if code != exitFailed || !strings.Contains(out, "exit code 7") || !strings.Contains(out, "log:") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestRunRefusesUnsupported(t *testing.T) {
	bad := writeWF(t, "b.yml", "name: b\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: some/action@v1\n")
	state := filepath.Join(t.TempDir(), "st")
	code, _, errOut := cli(t, os.Environ(), "run", bad, "--event", "push", "--local", t.TempDir(), "--state", state)
	if code != exitUnsupported || !strings.Contains(errOut, "unsupported: uses some/action") {
		t.Fatalf("%d %q", code, errOut)
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("nothing may run or be created for an unsupported workflow")
	}
}

func TestRunEventMustBeTriggered(t *testing.T) {
	p := writeWF(t, "p.yml", "name: p\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo\n")
	if code, _, e := cli(t, os.Environ(), "run", p, "--event", "schedule", "--local", t.TempDir()); code != exitUsage || !strings.Contains(e, "does not run on schedule") {
		t.Fatalf("%d %q", code, e)
	}
}

func TestRunDryRunFastGate(t *testing.T) {
	gate := filepath.Join(realWorkflows(), "fast-gate.yml")
	if _, err := os.Stat(gate); err != nil {
		t.Skip("fast-gate.yml not found:", err)
	}
	state := filepath.Join(t.TempDir(), "st")
	code, out, errOut := cli(t, os.Environ(), "run", gate, "--event", "pull_request", "--local", t.TempDir(), "--state", state,
		"--base-ref", "develop", "--head-ref", "feature", "--dry-run")
	if code != exitOK {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"job go:", "job web:", "job lint:", "job fast-build:"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("a dry run must not create the state folder")
	}
}

func TestDefaultStateDir(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)
	got := defaultStateDir([]string{"LOCALAPPDATA=/l"}, now, "fast-gate.yml")
	if filepath.ToSlash(got) != "/l/Unravis/ci/runs/20261125-103000-fast-gate" {
		t.Fatalf("got %s", got)
	}
	if got := defaultStateDir(nil, now, "x.yml"); !strings.HasPrefix(got, os.TempDir()) {
		t.Fatalf("fallback %s", got)
	}
}
