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
	panic("collector failed: password=" + crashPlanted)
}

// TestMain serves two jobs on every OS. A re-run of the test binary with the
// crash env var set is the crashing child of the crash tests (it never reaches
// m.Run). Otherwise the package's tests must never write to the real
// %ProgramData%; each test that wants the copy points it at a temporary folder.
func TestMain(m *testing.M) {
	if dir := os.Getenv(envCrashChildDir); dir != "" {
		crashChild(dir) // a panic ends the process
	}
	PublicConfigDir = func() string { return "" }
	os.Exit(m.Run())
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
	if err := os.MkdirAll(folder, 0o750); err != nil {
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
	if err := os.MkdirAll(cd, 0o750); err != nil {
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

// The crash folder and every file in it are asked to be private: the raw dump
// holds the panic text before it is cleaned.
func TestCrashCaptureSecuresTheFolderAndEveryFile(t *testing.T) {
	folder := t.TempDir()
	cd := CrashDir(folder)
	if err := os.MkdirAll(cd, 0o750); err != nil {
		t.Fatal(err)
	}
	// A raw dump an earlier run left, old enough for this start to clean it.
	raw := filepath.Join(cd, "crash-20261101T100000Z-9-go.pending")
	if err := os.WriteFile(raw, []byte("panic: boom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(raw, old, old); err != nil {
		t.Fatal(err)
	}
	var dirs, files []string
	sec := crashSecurity{
		dir:  func(p string) error { dirs = append(dirs, p); return nil },
		file: func(p string) error { files = append(files, filepath.Base(p)); return nil },
	}
	c := startCrashCaptureWith(folder, func(f string, a ...any) { t.Logf(f, a...) }, sec)
	if c == nil {
		t.Fatal("capture did not start")
	}
	t.Cleanup(c.Close)
	if len(dirs) != 1 || dirs[0] != cd {
		t.Fatalf("secured folders %v, want only %s", dirs, cd)
	}
	joined := strings.Join(files, " ")
	if !strings.Contains(joined, "crash-20261101T100000Z-9-go.txt") {
		t.Errorf("the cleaned file was not secured: %v", files)
	}
	if !strings.Contains(joined, ".pending") {
		t.Errorf("this run's raw dump file was not secured: %v", files)
	}
}

// If the folder cannot be made private, nothing is captured: no dump is better
// than a readable one.
func TestCrashCaptureDoesNotStartWhenTheFolderCannotBeSecured(t *testing.T) {
	folder := t.TempDir()
	sec := crashSecurity{
		dir:  func(string) error { return os.ErrPermission },
		file: func(string) error { return nil },
	}
	var logged string
	c := startCrashCaptureWith(folder, func(f string, a ...any) { logged = f }, sec)
	if c != nil {
		c.Close()
		t.Fatal("capture started in a folder that could not be secured")
	}
	if logged == "" {
		t.Error("the failure was not logged")
	}
	if ents, _ := os.ReadDir(CrashDir(folder)); len(ents) != 0 {
		t.Errorf("files were written to an unsecured folder: %v", ents)
	}
}

type failingEvents struct {
	err   error
	calls int
}

func (f *failingEvents) EventDurable(kind, detail string) error { f.calls++; return f.err }

// A full disk makes the event write fail: the files must stay unmarked so the
// next start reports the crash, and only a stored event marks them.
func TestCrashIsNotMarkedReportedWhenTheEventCannotBeStored(t *testing.T) {
	dir := t.TempDir()
	cd := CrashDir(dir)
	if err := os.MkdirAll(cd, 0o750); err != nil {
		t.Fatal(err)
	}
	name := "crash-20261101T100000Z-1-go.txt"
	if err := os.WriteFile(filepath.Join(cd, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	logf := func(string, ...any) {}
	bad := &failingEvents{err: os.ErrClosed}
	reportPreviousCrashes(bad, dir, logf)
	if bad.calls != 1 {
		t.Fatalf("event calls = %d", bad.calls)
	}
	if _, err := os.Stat(filepath.Join(cd, name)); err != nil {
		t.Fatalf("file was marked although the event was not stored: %v", err)
	}
	// The next start, with a disk that works, reports it and marks it.
	s := openT(t, dir, "r2", &clock{t: t0})
	reportPreviousCrashes(s, dir, logf)
	if evs := crashEvents(t, dir); len(evs) != 1 {
		t.Fatalf("crash events after the retry: %+v", evs)
	}
	if _, err := os.Stat(filepath.Join(cd, name)); err == nil {
		t.Fatal("file not marked after the event was stored")
	}
}

// The recordings are removed with "delete recordings" / uninstall: the crash
// files go with them, a stranger's file in that folder does not.
func TestRemoveRecordingsAlsoRemovesTheCrashFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	cd := CrashDir(dir)
	if err := os.MkdirAll(cd, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"crash-20261101T100000Z-1-go.pending", "crash-20261101T100000Z-1-go.txt", "crash-20261102T100000Z-2-go.reported.txt"} {
		if err := os.WriteFile(filepath.Join(cd, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, FileNameFor(t0)), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := RemoveRecordings(dir)
	if err != nil || n != 4 {
		t.Fatalf("removed %d, err %v", n, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("recordings folder kept although only crash files and recordings were in it")
	}

	// A file that is not the recorder's keeps its folder, and the folder above it.
	if err := os.MkdirAll(cd, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "crash-20261101T100000Z-1-go.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "mine.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err = RemoveRecordings(dir); err != nil || n != 1 {
		t.Fatalf("removed %d, err %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(cd, "mine.txt")); err != nil {
		t.Fatalf("a file that is not ours was removed: %v", err)
	}
}

func TestEventDurableStoresTheEventNow(t *testing.T) {
	dir := t.TempDir()
	s := openT(t, dir, "r1", &clock{t: t0})
	if err := s.EventDurable(EventCrashed, "crashes=1 at=x"); err != nil {
		t.Fatal(err)
	}
	if evs := crashEvents(t, dir); len(evs) != 1 {
		t.Fatalf("event not on disk: %+v", evs)
	}
}
