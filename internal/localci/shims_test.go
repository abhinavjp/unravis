package localci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runtimeIsWindows() bool { return runtime.GOOS == "windows" }

func newShimEnv(t *testing.T, with map[string]string) (*shimEnv, *[]string) {
	t.Helper()
	var logs []string
	return &shimEnv{
		With: with, Workdir: t.TempDir(), StateDir: t.TempDir(), TmpDir: t.TempDir(), RunnerOS: "Linux",
		Env:    os.Environ(),
		GitHub: map[string]any{},
		Logf:   func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}, &logs
}

func TestGoVersionOK(t *testing.T) {
	cases := []struct {
		found, want string
		ok          bool
	}{
		{"1.27.4", "1.27.4", true},
		{"1.27.4", "1.27", true},
		{"1.27.0", "1.27.4", false},
		{"1.27", "1.27.4", false},
		{"1.28.0", "1.27.4", true},
		{"1.9.0", "1.10", false},
		{"1.10.1", "1.9", true},
		{"1.27rc1", "1.27", true},
		{"1.26.9", "1.27", false},
		{"1.27.4", "stable", true},
		{"1.22.1", "1.22.x", true},
		{"1.21.1", "^1.22", false},
	}
	for _, c := range cases {
		got, err := goVersionOK(c.found, c.want)
		if err != nil || got != c.ok {
			t.Errorf("goVersionOK(%q,%q)=%v,%v want %v", c.found, c.want, got, err, c.ok)
		}
	}
	if _, err := goVersionOK("weird", "1.2"); err == nil {
		t.Error("want error for unreadable found version")
	}
}

func TestNodeVersionOK(t *testing.T) {
	cases := []struct {
		found, want string
		ok          bool
	}{
		{"22.23.5", "22.23.5", true},
		{"22.23.6", "22.23.5", true},
		{"22.24.0", "22.23.5", true},
		{"22.23.4", "22.23.5", true},
		{"22.1.0", "22.23.5", false},
		{"22.1.0", "22", true},
		{"23.0.0", "22", false},
		{"20.9.0", "22", false},
		{"22.23.5", "v22.x", true},
		{"22.5.0", "22.4", true},
		{"22.3.9", "22.4", false},
		{"22.0.0", "lts/*", true},
	}
	for _, c := range cases {
		got, err := nodeVersionOK(c.found, c.want)
		if err != nil || got != c.ok {
			t.Errorf("nodeVersionOK(%q,%q)=%v,%v want %v", c.found, c.want, got, err, c.ok)
		}
	}
}

func TestWantedGo(t *testing.T) {
	if got := wantedGo("module x\n\ngo 1.27.4\n"); got != "1.27.4" {
		t.Fatal(got)
	}
	if got := wantedGo("module x\ngo 1.22\ntoolchain go1.22.5\n"); got != "1.22.5" {
		t.Fatal(got)
	}
	if got := wantedGo("module x\ngo 1.22\ntoolchain default\n"); got != "1.22" {
		t.Fatal(got)
	}
}

func TestShimTableMatchesParserAllowList(t *testing.T) {
	for name := range allowedUses {
		if _, ok := shims[name]; !ok {
			t.Errorf("uses %s is allowed by the parser but has no shim", name)
		}
	}
	for name := range shims {
		if !allowedUses[name] {
			t.Errorf("shim %s is not allowed by the parser", name)
		}
	}
	if shimName("actions/checkout@abc # v7") != "actions/checkout" {
		t.Error("shimName")
	}
}

func TestShimCheckout(t *testing.T) {
	s, logs := newShimEnv(t, nil)
	if _, err := shimCheckout(context.Background(), s); err == nil || !strings.Contains(err.Error(), ".git") {
		t.Fatalf("want .git error, got %v", err)
	}
	// .git as a file (worktree) is accepted
	if err := os.WriteFile(filepath.Join(s.Workdir, ".git"), []byte("gitdir: x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := shimCheckout(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(*logs) == 0 {
		t.Fatal("expected a log line")
	}
}

func TestShimCheckoutLogsHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	s, logs := newShimEnv(t, nil)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		cmd := exec.Command("git", args...) //nolint:gosec // fixed git binary with test-controlled args
		cmd.Dir = s.Workdir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed: %v %s", args, err, out)
		}
	}
	if _, err := shimCheckout(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), " at ") {
		t.Fatalf("expected the HEAD sha in the log: %v", *logs)
	}
}

func TestShimSetupGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH:", err)
	}
	s, _ := newShimEnv(t, map[string]string{"go-version": "1.1"})
	out, err := shimSetupGo(context.Background(), s)
	if err != nil || out["go-version"] == "" {
		t.Fatalf("got %v %v", out, err)
	}
	s.With = map[string]string{"go-version": "99.1"}
	if _, err := shimSetupGo(context.Background(), s); err == nil || !strings.Contains(err.Error(), "setup-go: need go 99.1, found ") {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Workdir, "go.mod"), []byte("module x\ngo 99.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.With = map[string]string{"go-version-file": "go.mod"}
	if _, err := shimSetupGo(context.Background(), s); err == nil || !strings.Contains(err.Error(), "need go 99.0.1") {
		t.Fatalf("got %v", err)
	}
}

func TestShimSetupNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH:", err)
	}
	s, _ := newShimEnv(t, map[string]string{"node-version": "0"})
	// major 0 can never match a real node
	if _, err := shimSetupNode(context.Background(), s); err == nil || !strings.Contains(err.Error(), "setup-node: need node 0") {
		t.Fatalf("got %v", err)
	}
	s.With = map[string]string{}
	out, err := shimSetupNode(context.Background(), s)
	if err != nil || out["node-version"] == "" {
		t.Fatalf("got %v %v", out, err)
	}
}

func TestShimCache(t *testing.T) {
	s, _ := newShimEnv(t, nil)
	for _, name := range []string{"actions/cache", "actions/cache/restore", "actions/cache/save"} {
		out, err := shims[name](context.Background(), s)
		if err != nil || out["cache-hit"] != "false" {
			t.Fatalf("%s: %v %v", name, out, err)
		}
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArtifactRoundtrip(t *testing.T) {
	s, _ := newShimEnv(t, nil)
	writeFiles(t, s.Workdir, map[string]string{
		"out/a.txt":        "A",
		"out/sub/b.txt":    "B",
		"out/sub/skip.log": "L",
		"single.txt":       "S",
		"web/dist/x.js":    "X",
		"web/src/y.js":     "Y",
	})
	s.With = map[string]string{"name": "bundle", "path": "out\nsingle.txt\nweb/**/*.js\n!out/sub/skip.log\n!web/src"}
	if _, err := shimUpload(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got, _ := collectFiles(filepath.Join(s.StateDir, "artifacts", "bundle"), []string{"."})
	want := []string{"out/a.txt", "out/sub/b.txt", "single.txt", "web/dist/x.js"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uploaded %v want %v", got, want)
	}
	// download by name into a new path
	s.With = map[string]string{"name": "bundle", "path": "restored"}
	if _, err := shimDownload(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(s.Workdir, "restored", "out", "sub", "b.txt")); err != nil || string(b) != "B" {
		t.Fatalf("restored: %q %v", b, err)
	}
	// download everything: one folder per artifact, or merged
	s.With = map[string]string{"path": "all"}
	if _, err := shimDownload(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Workdir, "all", "bundle", "single.txt")); err != nil {
		t.Fatal(err)
	}
	s.With = map[string]string{"path": "merged", "merge-multiple": "true"}
	if _, err := shimDownload(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Workdir, "merged", "single.txt")); err != nil {
		t.Fatal(err)
	}
	s.With = map[string]string{"name": "nope"}
	if _, err := shimDownload(context.Background(), s); err == nil {
		t.Fatal("want error for a missing artifact")
	}
}

func TestUploadIfNoFilesFound(t *testing.T) {
	cases := []struct {
		mode    string
		wantErr bool
		wantLog bool
	}{
		{"", false, true}, {"warn", false, true}, {"ignore", false, false}, {"error", true, false},
	}
	for _, c := range cases {
		s, logs := newShimEnv(t, map[string]string{"name": "n", "path": "missing/*.txt", "if-no-files-found": c.mode})
		_, err := shimUpload(context.Background(), s)
		if (err != nil) != c.wantErr {
			t.Errorf("mode %q: err=%v", c.mode, err)
		}
		if (len(*logs) > 0) != c.wantLog {
			t.Errorf("mode %q: logs=%v", c.mode, *logs)
		}
		if _, err := os.Stat(filepath.Join(s.StateDir, "artifacts", "n")); err == nil {
			t.Errorf("mode %q: artifact folder must not exist", c.mode)
		}
	}
}

func TestUploadRejectsOutsidePathAndBadName(t *testing.T) {
	s, _ := newShimEnv(t, map[string]string{"name": "../x", "path": "a"})
	if _, err := shimUpload(context.Background(), s); err == nil {
		t.Fatal("want bad name error")
	}
	s.With = map[string]string{"name": "ok", "path": filepath.Join(t.TempDir(), "elsewhere.txt")}
	if _, err := shimUpload(context.Background(), s); err == nil {
		t.Fatal("want outside-workspace error")
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, name string
		ok        bool
	}{
		{"a/*.txt", "a/b.txt", true},
		{"a/*.txt", "a/c/b.txt", false},
		{"a/**/*.txt", "a/c/d/b.txt", true},
		{"a/**/*.txt", "a/b.txt", true},
		{"**", "x/y", true},
		{"*.go", "main.go", true},
		{"a/**", "a", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.pat, c.name); got != c.ok {
			t.Errorf("matchGlob(%q,%q)=%v", c.pat, c.name, got)
		}
	}
}

func TestShimTrufflehogMissingIsLoud(t *testing.T) {
	s, _ := newShimEnv(t, map[string]string{"extra_args": "--results=verified"})
	s.Env = []string{"PATH=" + t.TempDir()}
	_, err := shimTrufflehog(context.Background(), s)
	if err == nil || err.Error() != "trufflehog is not installed on this PC" {
		t.Fatalf("got %v", err)
	}
}

func TestShimTrufflehogArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the fake trufflehog")
	}
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\necho scanning\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "trufflehog"), []byte(script), 0o755); err != nil { //nolint:gosec // executable file the test needs to run
		t.Fatal(err)
	}
	s, logs := newShimEnv(t, map[string]string{"extra_args": "--results=verified,unknown --exclude-paths=x.txt"})
	s.Env = []string{"PATH=" + bin}
	s.GitHub = map[string]any{"event": map[string]any{"pull_request": map[string]any{
		"base": map[string]any{"sha": "BASE"}, "head": map[string]any{"sha": "HEAD1"}}}}
	if _, err := shimTrufflehog(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argsFile) //nolint:gosec // path built by the tool/test itself, reading it is the point
	want := "git file://" + filepath.ToSlash(s.Workdir) + " --since-commit BASE --branch HEAD1 --fail --no-update --results=verified,unknown --exclude-paths=x.txt"
	if strings.TrimSpace(string(b)) != want {
		t.Fatalf("args\n got %q\nwant %q", strings.TrimSpace(string(b)), want)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "scanning") {
		t.Fatalf("output not logged: %v", *logs)
	}
}

func TestCollectFilesRejectsDotDot(t *testing.T) {
	s, _ := newShimEnv(t, nil)
	writeFiles(t, s.Workdir, map[string]string{"a/x.txt": "x", "..foo/y.txt": "y", "..bar": "b"})
	for _, pat := range []string{"../x", "../*.yml", "a/../../x", "a/../x.txt", "./../x", "!a\n../x"} {
		if _, err := collectFiles(s.Workdir, strings.Split(pat, "\n")); err == nil {
			t.Errorf("pattern %q must be rejected", pat)
		}
	}
	got, err := collectFiles(s.Workdir, []string{"..foo", "..bar"})
	if err != nil || !reflect.DeepEqual(got, []string{"..bar", "..foo/y.txt"}) {
		t.Fatalf("..foo and ..bar are ordinary names: %v %v", got, err)
	}
	// an absolute path that climbs out is refused, one that stays inside works
	if _, err := collectFiles(s.Workdir, []string{filepath.Join(s.Workdir, "a", "..", "..", "x")}); err == nil {
		t.Error("absolute path leaving the workspace must be rejected")
	}
	if got, err := collectFiles(s.Workdir, []string{filepath.Join(s.Workdir, "a", "x.txt")}); err != nil || len(got) != 1 {
		t.Errorf("absolute path inside the workspace: %v %v", got, err)
	}
}

func TestUploadAndDownloadRejectTraversal(t *testing.T) {
	ctx := context.Background()
	s, _ := newShimEnv(t, map[string]string{"name": "n", "path": "../x"})
	if _, err := shimUpload(ctx, s); err == nil {
		t.Fatal("upload of ../x must fail")
	}
	s.With = map[string]string{"name": ".", "path": "a"}
	if _, err := shimUpload(ctx, s); err == nil {
		t.Fatal(`artifact name "." must be refused`)
	}
	writeFiles(t, s.Workdir, map[string]string{"a.txt": "A"})
	s.With = map[string]string{"name": "ok", "path": "a.txt"}
	if _, err := shimUpload(ctx, s); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../out", "sub/../../out", filepath.Join(t.TempDir(), "elsewhere")} {
		s.With = map[string]string{"name": "ok", "path": p}
		if _, err := shimDownload(ctx, s); err == nil {
			t.Errorf("download to %q must be refused", p)
		}
	}
	s.With = map[string]string{"name": "ok", "path": filepath.Join(s.Workdir, "inside")}
	if _, err := shimDownload(ctx, s); err != nil {
		t.Fatalf("an absolute target inside the workspace is fine: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Workdir, "inside", "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := joinInside(s.StateDir, "../x"); err == nil {
		t.Fatal("joinInside must refuse ..")
	}
	if p, err := joinInside(s.StateDir, "..foo/x"); err != nil || !strings.HasPrefix(p, s.StateDir) {
		t.Fatalf("..foo is legal: %q %v", p, err)
	}
}

// fakeTrufflehog puts a tiny scanner on a PATH folder and returns that folder.
func fakeTrufflehog(t *testing.T, body string) string {
	t.Helper()
	bin := t.TempDir()
	name, script := "trufflehog", "#!/bin/sh\n"+body+"\n"
	if runtime.GOOS == "windows" {
		name, script = "trufflehog.cmd", "@echo off\r\n"+strings.ReplaceAll(body, "\n", "\r\n")+"\r\n"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil { //nolint:gosec // executable file the test needs to run
		t.Fatal(err)
	}
	return bin
}

func TestShimTrufflehogRunsToCompletionInsideItsTree(t *testing.T) {
	body := "echo scanning\nexit 0"
	if runtime.GOOS == "windows" {
		body = "echo scanning\nexit /b 0"
	}
	bin := fakeTrufflehog(t, body)
	s, logs := newShimEnv(t, nil)
	s.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	done := make(chan error, 1)
	go func() {
		_, err := shimTrufflehog(context.Background(), s)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the scanner hung: its process was never resumed or released")
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "scanning") {
		t.Fatalf("logs %v", *logs)
	}
}

func TestRunCaptureReturnsOutput(t *testing.T) {
	s, _ := newShimEnv(t, nil)
	out, err := s.runCapture(context.Background(), "go", "version")
	if err != nil || !strings.HasPrefix(out, "go version") {
		t.Fatalf("%q %v", out, err)
	}
}
