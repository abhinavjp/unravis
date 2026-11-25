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

func TestScrubEnvIsAnAllowList(t *testing.T) {
	kept := []string{
		"PATH=/usr/bin", "Path=C:/bin", "SystemRoot=C:/Windows", "TEMP=C:/t", "USERPROFILE=C:/u", "GOPATH=/g", "GOCACHE=/c",
		"GOMODCACHE=/m", "HOME=/h", "LOCALAPPDATA=C:/l", "=C:=C:/", "LC_ALL=C", "ComSpec=C:/cmd.exe", "PATHEXT=.EXE",
	}
	dropped := []string{
		// secret names
		"GH_TOKEN=x", "GITHUB_TOKEN=x", "gh_token=x", "NPM_TOKEN=x", "my_secret=x", "DB_PASSWORD=x", "SIGNING_PASSPHRASE=x",
		"GPG_PASSPHRASE_FILE=x", "PROTON_BRIDGE_USER=x", "ProtonPass=x", "SIGNPATH_API_TOKEN=x", "SIGNPATH_ORG=x",
		"UNRAVIS_RELEASE_KEY=x", "unravis_release=x", "AWS_SESSION_TOKEN=x", "AWS_ACCESS_KEY_ID=x", "STRIPE_API_KEY=x",
		"GOOGLE_APPLICATION_CREDENTIALS=x",
		// names nobody listed: a deny-list would keep these, an allow-list does not
		"MYSQL_PWD=x", "DATABASE_URL=postgres://u:p@h/db", "SSH_AUTH_SOCK=/s", "GITHUB_WORKSPACE=/w", "UNRAVIS_HOME=1",
		"GIT_ASKPASS=/a", "GIT_CONFIG_GLOBAL=/g", "NODE_OPTIONS=--x", "TOKENIZER=1",
	}
	got := scrubEnv(append(append([]string{}, dropped...), kept...), nil)
	if !reflect.DeepEqual(got, kept) {
		t.Fatalf("scrubEnv\n got %v\nwant %v", got, kept)
	}
}

func TestScrubEnvExtraAllow(t *testing.T) {
	env := []string{"PATH=/bin", "MY_FLAG=1", "OTHER=2", "DEPLOY_TOKEN=sekret-value"}
	got := scrubEnv(env, []string{"my_flag", " DEPLOY_TOKEN "})
	want := []string{"PATH=/bin", "MY_FLAG=1", "DEPLOY_TOKEN=sekret-value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// a name that is not listed stays out, and the extra list is not a pattern
	if got := scrubEnv(env, []string{"MY"}); !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Fatalf("got %v", got)
	}
	// allowing a secret by name keeps it out of every log all the same
	if red := newRedactor(secretValues(env)); red("x sekret-value y") != "x *** y" {
		t.Fatal("an explicitly allowed secret must still be masked")
	}
}

func TestSecretsIncludeTheGhLoginToken(t *testing.T) {
	old := ghAuthToken
	t.Cleanup(func() { ghAuthToken = old })
	ghAuthToken = func([]string) string { return "gho_loginTokenValue123" }
	vals := secrets([]string{"NPM_TOKEN=abcdef123", "PATH=/bin"})
	if len(vals) != 2 || vals[0] != "gho_loginTokenValue123" || vals[1] != "abcdef123" {
		t.Fatalf("values %q", vals)
	}
	if got := newRedactor(vals)("a gho_loginTokenValue123 b abcdef123"); got != "a *** b ***" {
		t.Fatalf("got %q", got)
	}
	ghAuthToken = func([]string) string { return "" } // no gh, or not logged in: nothing added
	if vals := secrets([]string{"PATH=/bin"}); len(vals) != 0 {
		t.Fatalf("values %q", vals)
	}
}

func TestMain(m *testing.M) {
	// the tests never run the real gh
	ghAuthToken = func([]string) string { return "" }
	installStatusFakes() // nor post to, read from or start anything on GitHub
	os.Exit(m.Run())
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
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil { //nolint:gosec // a file in the test's own temp folder
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

// TestRunDryRunFastGate plans the real fast-gate.yml as a pull request, whatever its on: block says: it holds
// before and after the cutover (step 2 leaves workflow_dispatch only; TestRunDryRunDispatchOnlyFastGate pins that shape).
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

// dispatchOnlyGate is the fast gate as the cutover leaves it: the real file with its on: block replaced by
// workflow_dispatch alone (docs/ci/CUTOVER.md, step 2). It skips the test when the real file has no such block.
func dispatchOnlyGate(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(realWorkflows(), "fast-gate.yml"))
	if err != nil {
		t.Skip("fast-gate.yml not found:", err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	start := strings.Index(src, "\non:\n")
	end := strings.Index(src, "\npermissions:")
	if start < 0 || end < start {
		t.Skip("fast-gate.yml has no on: block followed by permissions:")
	}
	return writeWF(t, "fast-gate.yml", src[:start]+"\non:\n  workflow_dispatch:\n"+src[end:])
}

// A gate that only has workflow_dispatch in on: (what GitHub keeps after the cutover) plans exactly like one
// with pull_request, for both events the local pipeline starts it for, and still refuses an event it cannot be.
func TestRunDryRunDispatchOnlyFastGate(t *testing.T) {
	gate := dispatchOnlyGate(t)
	for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
		t.Run(event, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "st")
			code, out, errOut := cli(t, os.Environ(), "run", gate, "--event", event, "--local", t.TempDir(), "--state", state,
				"--base-ref", "develop", "--head-ref", "feature", "--dry-run")
			if code != exitOK {
				t.Fatalf("%d\n%s\n%s", code, out, errOut)
			}
			for _, want := range []string{"job go:", "job web:", "job lint:", "job fast-build:"} {
				if !strings.Contains(out, want) {
					t.Errorf("plan lacks %q:\n%s", want, out)
				}
			}
		})
	}
	if code, _, e := cli(t, os.Environ(), "run", gate, "--event", "schedule", "--local", t.TempDir(), "--dry-run"); code != exitUsage || !strings.Contains(e, "does not run on schedule") {
		t.Fatalf("a dispatch-only workflow is not a scheduled one: %d %q", code, e)
	}
}

func TestRunEventTriggerRules(t *testing.T) {
	body := "\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo\n"
	for name, tc := range map[string]struct {
		on, event string
		want      int
	}{
		"the event is listed":                {"on:\n  push:", "push", exitOK},
		"dispatch only, as a pull request":   {"on:\n  workflow_dispatch:", "pull_request", exitOK},
		"dispatch only, as a push":           {"on:\n  workflow_dispatch:", "push", exitOK},
		"dispatch only, as a schedule":       {"on:\n  workflow_dispatch:", "schedule", exitUsage},
		"pull_request only, as a push":       {"on:\n  pull_request:", "push", exitUsage},
		"schedule only, as a pull request":   {"on:\n  schedule:\n    - cron: '0 3 * * *'", "pull_request", exitUsage},
		"schedule only, as a dispatch":       {"on:\n  schedule:\n    - cron: '0 3 * * *'", "workflow_dispatch", exitUsage},
		"empty on: runs as any event":        {"on: {}", "pull_request", exitOK},
		"no on: key runs as any event":       {"", "push", exitOK},
		"dispatch plus push, as a dispatch":  {"on:\n  push:\n  workflow_dispatch:", "workflow_dispatch", exitOK},
		"push only, as a manual dispatch":    {"on:\n  push:", "workflow_dispatch", exitUsage},
		"dispatch plus push, as a schedule":  {"on:\n  push:\n  workflow_dispatch:", "schedule", exitUsage},
		"dispatch plus schedule as schedule": {"on:\n  schedule:\n    - cron: '0 3 * * *'\n  workflow_dispatch:", "schedule", exitOK},
	} {
		t.Run(name, func(t *testing.T) {
			p := writeWF(t, "w.yml", "name: w\n"+tc.on+body)
			code, out, e := cli(t, os.Environ(), "run", p, "--event", tc.event, "--local", t.TempDir(), "--dry-run")
			if code != tc.want {
				t.Fatalf("want %d, got %d\n%s\n%s", tc.want, code, out, e)
			}
			if tc.want == exitUsage && !strings.Contains(e, "does not run on "+tc.event) {
				t.Fatalf("the refusal must say why: %q", e)
			}
		})
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

func TestSecretValuesAndRedactor(t *testing.T) {
	env := []string{"NPM_TOKEN=abcdef123", "SHORT_TOKEN=abc", "PATH=/bin:/usr/bin", "DB_PASSWORD=longer-password-value", "=weird", "NOEQ"}
	vals := secretValues(env)
	if len(vals) != 2 || vals[0] != "longer-password-value" || vals[1] != "abcdef123" {
		t.Fatalf("values %q: want the two long secret values, longest first", vals)
	}
	red := newRedactor(vals)
	if got := red("a abcdef123 b longer-password-value c abc"); got != "a *** b *** c abc" {
		t.Fatalf("got %q", got)
	}
}

func TestRunNeverWritesScrubbedSecretsAnywhere(t *testing.T) {
	needBash(t)
	const secret = "leakyvalue123"
	work := checkout(t)
	wf := writeWF(t, "s.yml", `name: s
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    outputs:
      o: ${{ steps.s.outputs.v }}
    steps:
      - id: s
        run: |
          v=leaky; v="${v}value123"
          echo "out $v"
          echo "err $v" >&2
          echo "::error::bad $v"
          echo "v=$v" >> "$GITHUB_OUTPUT"
          echo "sum $v" >> "$GITHUB_STEP_SUMMARY"
      - run: |
          echo clean
          exit 1
`)
	state := filepath.Join(t.TempDir(), "st")
	code, out, errOut := cli(t, append(os.Environ(), "NPM_TOKEN="+secret), "run", wf, "--event", "push", "--local", work, "--state", state)
	if code != exitFailed {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
		t.Fatalf("console leaked the secret:\n%s\n%s", out, errOut)
	}
	n := 0
	err := filepath.WalkDir(state, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, rerr := os.ReadFile(p) //nolint:gosec // reading the test's own state folder
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), secret) {
			t.Errorf("%s leaked the secret:\n%s", p, b)
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("walk: %v (%d files)", err, n)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "run.json")); !strings.Contains(string(b), `"o": "***"`) { //nolint:gosec // reading the test's own state folder
		t.Fatalf("job output must be masked in run.json: %s", b)
	}
}

func TestRunKeepsOnlyAllowedVariables(t *testing.T) {
	needBash(t)
	work := checkout(t)
	wf := writeWF(t, "e.yml", `name: e
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - run: echo "kept=[$KEEP_ME] dropped=[$DROP_ME] db=[$DATABASE_URL] path=${PATH:+set}"
`)
	env := append(os.Environ(), "KEEP_ME=one", "DROP_ME=two", "DATABASE_URL=postgres://u:pw@h/db")
	state := filepath.Join(t.TempDir(), "st")
	code, out, errOut := cli(t, env, "run", wf, "--event", "push", "--local", work, "--state", state, "--allow-env", "KEEP_ME")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	logs, _ := filepath.Glob(filepath.Join(state, "*", "*", "*.log"))
	all := out
	for _, l := range logs {
		b, _ := os.ReadFile(l) //nolint:gosec // a log file the test just listed
		all += string(b)
	}
	if !strings.Contains(all, "kept=[one] dropped=[] db=[] path=set") {
		t.Fatalf("only the allowed variables reach a step:\n%s", all)
	}
}
