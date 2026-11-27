package hscli

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"api-audit-explorer/internal/cicli"
)

// fakeEngine records what it was called with and answers with a fixed exit code, some stdout and some stderr.
type fakeEngine struct {
	got  []string
	code int
}

func (f *fakeEngine) run(_ context.Context, args []string, stdout, stderr io.Writer, _ []string) int {
	f.got = append([]string{}, args...)
	_, _ = stdout.Write([]byte("engine says hello\n"))
	_, _ = stderr.Write([]byte("engine says sorry\n"))
	return f.code
}

func TestCIShimForwardsArgumentsAndExitCode(t *testing.T) {
	for _, code := range []int{0, 1, 2, 3, 5, 7} {
		fe := &fakeEngine{code: code}
		var out, errb bytes.Buffer
		args := []string{"queue", "list", "--json", "--state", "X"}
		if got := CIShim(context.Background(), fe.run, args, &out, &errb, nil); got != code {
			t.Errorf("exit %d came back as %d", code, got)
		}
		if !reflect.DeepEqual(fe.got, args) {
			t.Errorf("the engine got %v, want %v", fe.got, args)
		}
		if out.String() != "engine says hello\n" {
			t.Errorf("stdout is not the engine's alone: %q", out.String())
		}
	}
}

func TestCIShimNoticeGoesToStderrOnly(t *testing.T) {
	fe := &fakeEngine{}
	var out, errb bytes.Buffer
	CIShim(context.Background(), fe.run, []string{"status"}, &out, &errb, nil)
	if strings.Contains(out.String(), "homestretch") || strings.Contains(out.String(), "forwarding") {
		t.Fatalf("the notice leaked to stdout: %q", out.String())
	}
	want := "unravis-ci is now `homestretch ci`; forwarding\nengine says sorry\n"
	if errb.String() != want {
		t.Fatalf("stderr = %q, want %q", errb.String(), want)
	}
	if strings.Count(errb.String(), "forwarding") != 1 {
		t.Fatalf("the notice must be one line, once: %q", errb.String())
	}
}

// The agent starts the engine on itself as `run-item <id> --child`; that is not someone typing the old name, so no line.
func TestCIShimIsSilentForTheAgentsChildCall(t *testing.T) {
	fe := &fakeEngine{}
	var out, errb bytes.Buffer
	CIShim(context.Background(), fe.run, []string{"run-item", "r1", "--child", "--state", "S"}, &out, &errb, nil)
	if errb.String() != "engine says sorry\n" {
		t.Fatalf("a child call got the notice: %q", errb.String())
	}
	// A person typing run-item without --child still gets it.
	errb.Reset()
	CIShim(context.Background(), fe.run, []string{"run-item", "r1"}, &out, &errb, nil)
	if !strings.HasPrefix(errb.String(), CINotice) {
		t.Fatalf("run-item without --child lost the notice: %q", errb.String())
	}
}

// Through the real engine: stdout and the exit code are byte for byte what cicli.Main gives, only stderr has one more line.
func TestCIShimMatchesTheRealEngine(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"no-such-command"}, {}} {
		var wantOut, wantErr, out, errb bytes.Buffer
		want := cicli.Main(context.Background(), args, &wantOut, &wantErr, nil)
		got := CIShim(context.Background(), cicli.Main, args, &out, &errb, nil)
		if got != want || out.String() != wantOut.String() {
			t.Errorf("%v: exit %d stdout %q, the engine gives exit %d stdout %q", args, got, out.String(), want, wantOut.String())
		}
		if errb.String() != CINotice+"\n"+wantErr.String() {
			t.Errorf("%v: stderr %q is not the notice plus the engine's %q", args, errb.String(), wantErr.String())
		}
	}
	var out bytes.Buffer
	if code := CIShim(context.Background(), cicli.Main, []string{"version"}, &out, io.Discard, nil); code != 0 || out.String() != "unravis-ci "+cicli.Version()+"\n" {
		t.Fatalf("version through the shim: %d %q", code, out.String())
	}
}

// ---------------------------------------------------------------- scripts name programs that exist

// repoRoot finds the checkout from the test's folder (internal/hscli).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found above the test folder")
	return ""
}

// cmdRef matches a build or run target such as ./cmd/homestretch or 'cmd\homestretch\main.go' in a script.
var cmdRef = regexp.MustCompile(`(?:^|[\s'"(=,@])\.?[/\\]?cmd[/\\]([A-Za-z0-9_-]+)`)

// Every script that builds a program from cmd/<name> must name one that is there. This is what fails when a program is
// renamed or removed and a script still builds the old name.
func TestScriptsBuildProgramsThatExist(t *testing.T) {
	root := repoRoot(t)
	var scripts []string
	for _, pat := range []string{"*.ps1", "*.cmd", "*.sh"} {
		m, _ := filepath.Glob(filepath.Join(root, pat))
		scripts = append(scripts, m...)
	}
	for _, sub := range []string{"tools", filepath.Join(".github", "workflows")} {
		_ = filepath.WalkDir(filepath.Join(root, sub), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ps1", ".cmd", ".sh", ".yml":
				scripts = append(scripts, p)
			}
			return nil
		})
	}
	if len(scripts) < 5 {
		t.Fatalf("found only %d scripts: the scan is not looking in the right place", len(scripts))
	}
	seen := map[string]bool{}
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range cmdRef.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			seen[name] = true
			if _, err := os.Stat(filepath.Join(root, "cmd", name)); err != nil {
				rel, _ := filepath.Rel(root, s)
				t.Errorf("%s builds cmd/%s, which does not exist", filepath.ToSlash(rel), name)
			}
		}
	}
	for _, must := range []string{"homestretch", "unravis-releases"} {
		if !seen[must] {
			t.Errorf("no script builds cmd/%s: the scan or the scripts changed", must)
		}
	}
}

// The scripts that used to build or run unravis-ci use the new name, and the files they call are there.
func TestScriptsUseTheNewNames(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if s := read("tools/ci/prepush.sh"); !strings.Contains(s, "./cmd/homestretch") || strings.Contains(s, "./cmd/unravis-ci") || !strings.Contains(s, "ci exclusive") {
		t.Error("tools/ci/prepush.sh should build ./cmd/homestretch and run `ci exclusive`")
	}
	if s := read("tools/release/Promote-Develop.ps1"); !strings.Contains(s, `cmd\homestretch\main.go`) || !strings.Contains(s, "./cmd/homestretch") || strings.Contains(s, "./cmd/unravis-ci") {
		t.Error("Promote-Develop.ps1 should build ./cmd/homestretch for the hold check")
	}
	if s := read("build.ps1"); !strings.Contains(s, "./cmd/homestretch") || !strings.Contains(s, `dist\hs.exe`) || !strings.Contains(s, `dist\unravis-releases.exe`) {
		t.Error("build.ps1 should build homestretch.exe and hs.exe and still build unravis-releases.exe")
	}
	// The old launcher forwards to the new one, and the new one starts the dashboard program that setup installs.
	if s := read("Start Releases.cmd"); !strings.Contains(s, "Start Homestretch.cmd") {
		t.Error("Start Releases.cmd should call Start Homestretch.cmd")
	}
	if s := read("Start Homestretch.cmd"); !strings.Contains(s, `.\cmd\unravis-releases`) || !strings.Contains(s, `dist\unravis-releases.exe`) {
		t.Error("Start Homestretch.cmd should build and start the dashboard")
	}
	for _, rel := range []string{"Start Homestretch.cmd", "cmd/homestretch/main.go", "cmd/unravis-ci/main.go", "cmd/unravis-releases/main.go"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is missing", rel)
		}
	}
}
