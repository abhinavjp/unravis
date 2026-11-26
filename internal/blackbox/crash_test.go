package blackbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RV-BB-10: a crash of the recorder leaves a cleaned crash file, and the next
// start says so in the recording, once.

const (
	envCrashChildDir = "BLACKBOX_CRASH_CHILD_DIR"
	crashPlanted     = "hunter2-Planted-Secret-9f3a"
)

// crashChild is the test binary acting as a crashing recorder: it turns the
// capture on exactly as RunHost does, then panics without recovering.
func crashChild(folder string) {
	defer startCrashCapture(folder, func(string, ...any) {}).CloseUnlessPanic()
	time.Sleep(20 * time.Millisecond)
	panic("collector failed: password=" + crashPlanted)
}

func crashEvents(t *testing.T, folder string) []Event {
	t.Helper()
	m, err := ReadManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, e := range m.Events {
		if e.Kind == EventCrashed {
			out = append(out, e)
		}
	}
	return out
}

func runHostOnce(t *testing.T, g *hostRig) {
	t.Helper()
	o := g.opts()
	o.CrashCapture = true
	g.start(t, o)
	select {
	case <-g.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server never listened")
	}
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
}

func TestCrashedRecorderIsReportedOnceAtTheNextStart(t *testing.T) {
	g := newHostRig(t)
	folder := g.cfg.Folder
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}

	// A real crash: a child process panics with a planted secret in the message.
	cmd := exec.Command(os.Args[0], "-test.run=NONE") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), envCrashChildDir+"="+folder)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the child did not crash: %s", out)
	}
	pend, _ := filepath.Glob(filepath.Join(CrashDir(folder), "*.pending"))
	if len(pend) != 1 {
		t.Fatalf("expected one raw dump, got %v", pend)
	}
	old := time.Now().Add(-time.Minute) // past the grace
	if err := os.Chtimes(pend[0], old, old); err != nil {
		t.Fatal(err)
	}

	runHostOnce(t, g)

	evs := crashEvents(t, folder)
	if len(evs) != 1 || !strings.HasPrefix(evs[0].Detail, "crashes=1 at=") {
		t.Fatalf("crash events: %+v", evs)
	}
	files, _ := filepath.Glob(filepath.Join(CrashDir(folder), "crash-*.txt"))
	if len(files) != 1 || !strings.HasSuffix(files[0], ".reported.txt") {
		t.Fatalf("crash files: %v", files)
	}
	text, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "collector failed") || !strings.Contains(string(text), "goroutine") {
		t.Fatalf("the crash file does not carry the panic and the stacks:\n%s", text)
	}
	if strings.Contains(string(text), crashPlanted) {
		t.Fatal("the crash file kept the planted secret")
	}
	if left, _ := filepath.Glob(filepath.Join(CrashDir(folder), "*.pending")); len(left) != 0 {
		t.Fatalf("raw dump left behind: %v", left)
	}

	// A start after that reports nothing new.
	g2 := newHostRig(t)
	g2.cfg.Folder = folder
	runHostOnce(t, g2)
	if evs := crashEvents(t, folder); len(evs) != 1 {
		t.Fatalf("a second start reported the same crash again: %+v", evs)
	}
}

func TestSeveralCrashFilesAreOneEventAndEachIsReportedOnce(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	cd := CrashDir(dir)
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"crash-20261101T100000Z-1-go.txt", "crash-20261102T100000Z-2-go.txt", "crash-20261103T100000Z-3-go.txt"} {
		if err := os.WriteFile(filepath.Join(cd, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reportPreviousCrashes(s, dir, func(string, ...any) {})
	reportPreviousCrashes(s, dir, func(string, ...any) {})
	evs := crashEvents(t, dir)
	if len(evs) != 1 || !strings.HasPrefix(evs[0].Detail, "crashes=3 at=") {
		t.Fatalf("crash events: %+v", evs)
	}
}
