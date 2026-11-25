package crashlog

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	envChildDir = "CRASHLOG_TEST_CHILD_DIR"
	// plantedSecret is the shape the log sanitizer masks (a keyed value). The child puts it in its panic message.
	plantedSecret = "hunter2-Planted-Secret-9f3a"
)

// crashChildBlocked is parked in its own goroutine so the dump can show the "all goroutines" level took effect.
//
//go:noinline
func crashChildBlocked(stop chan struct{}) { <-stop }

// TestMain lets the test binary act as the crashing program: with envChildDir set it starts the capture and panics instead of running
// the tests.
func TestMain(m *testing.M) {
	if dir := os.Getenv(envChildDir); dir != "" {
		crashChild(dir)
		os.Exit(0) // not reached: the panic ends the process
	}
	os.Exit(m.Run())
}

func crashChild(dir string) {
	if _, err := Start(Options{Dir: dir, Grace: -1}); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(3)
	}
	go crashChildBlocked(make(chan struct{}))
	time.Sleep(50 * time.Millisecond) // let the goroutine park
	panic("connection failed: password=" + plantedSecret)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file it just made
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// A real panic in a real process leaves a raw dump, and finishing it leaves a sanitised trace file with the goroutine stacks.
func TestAPanicLeavesASanitisedTraceFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crashes")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe) //nolint:gosec // re-runs this test binary
	// GOTRACEBACK=single would print one goroutine; Start must raise it to all.
	cmd.Env = append(os.Environ(), envChildDir+"="+dir, "GOTRACEBACK=single")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("child should die of a panic (exit 2), got %v\n%s", err, out)
	}

	// Before the sweep there is one raw file: the runtime wrote it, so it still holds the secret. It is owner-only (not asserted
	// here; see the secure-callback test) and is not the file to share.
	var pending string
	for _, n := range names(t, dir) {
		if strings.HasSuffix(n, ".pending") {
			pending = filepath.Join(dir, n)
		}
	}
	if pending == "" {
		t.Fatalf("no pending crash file after the panic: %v\n%s", names(t, dir), out)
	}
	if !strings.Contains(read(t, pending), plantedSecret) {
		t.Fatalf("the raw dump should still hold the planted secret (the test would prove nothing): %s", read(t, pending))
	}

	if err := Sweep(Options{Dir: dir, Grace: -1}); err != nil {
		t.Fatal(err)
	}
	var finished []string
	for _, n := range names(t, dir) {
		if strings.HasSuffix(n, ".pending") {
			t.Errorf("raw file %s left after the sweep", n)
		}
		if strings.HasPrefix(n, "crash-") && strings.HasSuffix(n, "-go.txt") {
			finished = append(finished, n)
		}
	}
	if len(finished) != 1 {
		t.Fatalf("want one finished crash file, got %v", names(t, dir))
	}
	text := read(t, filepath.Join(dir, finished[0]))
	if strings.Contains(text, plantedSecret) {
		t.Errorf("the finished crash file still holds the secret:\n%s", text)
	}
	if !strings.Contains(text, "panic:") || !strings.Contains(text, "goroutine ") {
		t.Errorf("no panic line or goroutine trace in:\n%s", text)
	}
	if !strings.Contains(text, "crashChildBlocked") {
		t.Errorf("the parked goroutine is missing, so the traceback level is not \"all\":\n%s", text)
	}
}

func touch(t *testing.T, dir, name string, size int, mod time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestPruneKeepsTheNewestTen(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		touch(t, dir, fmt.Sprintf("crash-%02d-go.txt", i), 10, base.Add(time.Duration(i)*time.Hour))
	}
	touch(t, dir, "notes.txt", 10, base.Add(-48*time.Hour)) // not ours: never pruned
	if err := Prune(Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range names(t, dir) {
		got[n] = true
	}
	if len(got) != 11 || !got["notes.txt"] {
		t.Fatalf("want 10 crash files and notes.txt, got %v", names(t, dir))
	}
	for i := 0; i < 12; i++ {
		n := fmt.Sprintf("crash-%02d-go.txt", i)
		if want := i >= 2; got[n] != want {
			t.Errorf("%s kept=%v, want %v", n, got[n], want)
		}
	}
}

func TestPruneHonoursTheByteLimitAndAlwaysKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	touch(t, dir, "crash-1-go.txt", 40, base)
	touch(t, dir, "crash-2-go.txt", 40, base.Add(time.Hour))
	touch(t, dir, "crash-3-rust.dmp", 40, base.Add(2*time.Hour))
	if err := Prune(Options{Dir: dir, MaxBytes: 100}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 2 || got[0] != "crash-2-go.txt" || got[1] != "crash-3-rust.dmp" {
		t.Fatalf("got %v", got)
	}
	if err := Prune(Options{Dir: dir, MaxBytes: 10}); err != nil { // the newest alone is over the limit
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "crash-3-rust.dmp" {
		t.Fatalf("got %v", got)
	}
}

func TestStartPrunesAtStartUpAndCloseRemovesTheEmptyFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crashes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 12; i++ {
		touch(t, dir, fmt.Sprintf("crash-%02d-go.txt", i), 5, base.Add(time.Duration(i)*time.Minute))
	}
	c, err := Start(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if c.Path() == "" {
		t.Fatal("no pending file")
	}
	if n := len(names(t, dir)); n != 11 { // 10 kept + this run's pending file
		t.Fatalf("want 11 entries, got %d: %v", n, names(t, dir))
	}
	c.Close()
	c.Close() // twice is fine
	(*Capture)(nil).Close()
	if n := len(names(t, dir)); n != 10 {
		t.Fatalf("a clean run should leave only the 10 kept files, got %v", names(t, dir))
	}
}

func TestSweepSkipsAYoungPendingFileAndRemovesAnOldEmptyOne(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	touch(t, dir, "crash-a-1-go.pending", 0, now.Add(-time.Hour))    // abandoned empty file
	touch(t, dir, "crash-b-2-go.pending", 20, now.Add(-time.Second)) // maybe a live copy's
	if err := Sweep(Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "crash-b-2-go.pending" {
		t.Fatalf("got %v", got)
	}
}

func TestFinishedFileIsSecuredAndKeepsTheCrashTime(t *testing.T) {
	dir := t.TempDir()
	when := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	touch(t, dir, "crash-a-1-go.pending", 20, when)
	var secured []string
	opts := Options{Dir: dir, Grace: -1, SecureFile: func(p string) error { secured = append(secured, filepath.Base(p)); return nil }}
	if err := Sweep(opts); err != nil {
		t.Fatal(err)
	}
	if len(secured) != 1 || secured[0] != "crash-a-1-go.txt" {
		t.Fatalf("secured %v", secured)
	}
	info, err := os.Stat(filepath.Join(dir, "crash-a-1-go.txt"))
	if err != nil || !info.ModTime().Equal(when) {
		t.Fatalf("mod time %v, err %v, want %v", info.ModTime(), err, when)
	}
}

// If the finished file cannot be made owner-only it is not left behind, and the raw file stays for the next try.
func TestAFailedSecureLeavesNoUnsecuredFinishedFile(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "crash-a-1-go.pending", 20, time.Now().Add(-time.Hour))
	opts := Options{Dir: dir, Grace: -1, SecureFile: func(string) error { return errors.New("no acl") }}
	if err := Sweep(opts); err == nil {
		t.Fatal("want an error")
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "crash-a-1-go.pending" {
		t.Fatalf("got %v", got)
	}
}

func TestStartNeedsAFolder(t *testing.T) {
	if c, err := Start(Options{}); err == nil || c != nil {
		t.Fatalf("got %v, %v", c, err)
	}
}
