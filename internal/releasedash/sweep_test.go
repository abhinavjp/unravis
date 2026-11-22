package releasedash

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fake values only. The tests print whether a value shows, never the value.
const (
	fakeToken = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	fakePass  = "hunter2hunter2xyz"
)

// REL-1: a stop reason is free text from the release script and can hold what a failed command printed.
func TestReadLocalHidesSecretsInReasons(t *testing.T) {
	dir := t.TempDir()
	st := `{"version":"9.9.9-rc.1","status":"stopped","stage":"Sign","reason":"sign failed: token ` + fakeToken + ` passphrase: ` + fakePass + ` run by bobsmith"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(st), 0o600); err != nil {
		t.Fatal(err)
	}
	au := `{"ts":"2026-11-22T10:00:00Z","event":"stopped","version":"9.9.9-rc.1","stage":"Sign","user":"bobsmith","machine":"BUILDBOX-7","from":"` + fakeToken + `","reason":"sign failed on BUILDBOX-7: SIGN_PASS=` + fakePass + ` ` + fakeToken + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(au), 0o600); err != nil {
		t.Fatal(err)
	}
	l := ReadLocal(dir)
	if l.Run == nil || len(l.Events) != 1 {
		t.Fatalf("run=%v events=%d", l.Run, len(l.Events))
	}
	all := []string{l.Run.Reason, l.Run.Stage, l.Events[0].Reason, l.Events[0].From, l.Events[0].Name, l.Events[0].Stage}
	for _, s := range all {
		for _, bad := range []string{fakeToken, fakePass, "bobsmith", "BUILDBOX-7"} {
			if strings.Contains(s, bad) {
				t.Errorf("a field still holds a hidden value (%d chars): %q", len(bad), s)
			}
		}
	}
	if !strings.Contains(l.Run.Reason, "sign failed") {
		t.Errorf("the readable part was lost: %q", l.Run.Reason)
	}
}

// REL-3: Go test names are redacted like the node and verify names.
func TestParseTestsHidesSecretsInGoNames(t *testing.T) {
	log := strings.Join([]string{
		"job\tGo test\t2026-11-22T07:00:00.1Z === RUN   TestUpload",
		"job\tGo test\t2026-11-22T07:00:00.3Z --- FAIL: TestUpload/" + fakeToken + "_bobsmith (0.00s)",
		"job\tGo test\t2026-11-22T07:00:00.4Z --- PASS: TestOther/bobsmith (0.00s)",
		"job\tGo test\t2026-11-22T07:00:00.5Z FAIL",
	}, "\n")
	res := ParseTests(strings.NewReader(log), []string{"bobsmith"})
	if len(res.Suites) != 1 {
		t.Fatalf("%+v", res)
	}
	s := res.Suites[0]
	var names []string
	for _, c := range s.Cases {
		names = append(names, c.Name)
	}
	for _, f := range s.Failures {
		names = append(names, f.Name)
	}
	if len(names) < 2 {
		t.Fatalf("no names parsed: %+v", s)
	}
	for _, n := range names {
		if strings.Contains(n, fakeToken) || strings.Contains(n, "bobsmith") {
			t.Errorf("a test name still holds a hidden value: %q", n)
		}
	}
}

// REL-4: shapes the Redactor missed.
func TestRedactorMoreSecretShapes(t *testing.T) {
	r := func() *Redactor { return NewRedactor() }
	for in, bad := range map[string]string{
		"sigtool sign --passphrase " + fakePass + " manifest.json":  fakePass,
		"set SIGN_PASS=" + fakePass:                                 fakePass,
		"UNRAVIS_RELEASE_NONCE=" + fakePass:                         fakePass,
		"PROTON_PASS_PIN=4321":                                      "4321",
		`{"seed":"` + fakePass + `"}`:                               fakePass,
		"curl -u bob:" + fakePass + " https://x.example":            fakePass,
		"git clone https://bob:" + fakePass + "@host.example/r.git": fakePass,
	} {
		if got := r().Line(in); strings.Contains(got, bad) {
			t.Errorf("%q -> %q still holds the value", in, got)
		}
	}
	// Ordinary lines stay readable.
	for _, ok := range []string{
		"git clone https://github.com/abhinavjp/unravis.git",
		"shipping: 3 files, mapping ok, ping 12ms",
		"go test -count=1 ./...",
		"manifest sha256 " + strings.Repeat("ab", 32),
	} {
		if got := r().Line(ok); got != ok {
			t.Errorf("%q changed to %q", ok, got)
		}
	}
}

// REL-2: a second call for the same unchanged file reads and redacts nothing again.
func TestReadLogIsCachedWhileFileIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	ld := filepath.Join(dir, logsDir)
	if err := os.MkdirAll(ld, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "ci-v9.9.9-rc.1-ci-123.log.gz"
	path := filepath.Join(ld, name)
	write := func(body string) {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		_, _ = gz.Write([]byte(body))
		_ = gz.Close()
		_ = f.Close()
	}
	write("job\tstep\tone\njob\tstep\ttwo\n")
	a, _, err := ReadLog(dir, name, nil)
	if err != nil || len(a) != 2 {
		t.Fatalf("%v %d", err, len(a))
	}
	b, _, _ := ReadLog(dir, name, nil)
	if &a[0] != &b[0] {
		t.Error("the second call read the file again instead of using the kept lines")
	}
	// Another machine name list is another answer.
	c, _, _ := ReadLog(dir, name, []string{"bobsmith"})
	if &a[0] == &c[0] {
		t.Error("a different names list must not share the kept lines")
	}
	// A changed file (new size and time) is read again.
	write("job\tstep\tone\njob\tstep\ttwo\njob\tstep\tthree\n")
	_ = os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	d, _, _ := ReadLog(dir, name, nil)
	if len(d) != 3 {
		t.Errorf("a changed log was not read again: %d lines", len(d))
	}
	// Many callers at once all get the answer and nothing races (run with -race).
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l, _, err := ReadLog(dir, name, nil); err != nil || len(l) != 3 {
				t.Errorf("%v %d", err, len(l))
			}
		}()
	}
	wg.Wait()
}
