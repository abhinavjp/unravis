package releasedash

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	au := `{"ts":"2026-11-22T10:00:00Z","event":"stopped","version":"9.9.9-rc.1","stage":"Sign","user":"bobsmith","machine":"BUILDBOX-7","reason":"sign failed on BUILDBOX-7: SIGN_PASS=` + fakePass + ` ` + fakeToken + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(au), 0o600); err != nil {
		t.Fatal(err)
	}
	l := ReadLocal(dir)
	if l.Run == nil || len(l.Events) != 1 {
		t.Fatalf("run=%v events=%d", l.Run, len(l.Events))
	}
	for _, s := range []string{l.Run.Reason, l.Events[0].Reason} {
		for _, bad := range []string{fakeToken, fakePass, "bobsmith", "BUILDBOX-7"} {
			if strings.Contains(s, bad) {
				t.Errorf("a reason still holds a hidden value (%d chars): %q", len(bad), s)
			}
		}
	}
	if !strings.Contains(l.Run.Reason, "sign failed") {
		t.Errorf("the readable part was lost: %q", l.Run.Reason)
	}
}

// The event name, stage and "from" are fixed words the program matches on: a user or machine called like one of them must not change them.
func TestReadLocalKeepsEventWordsWhenANameIsTheSame(t *testing.T) {
	dir := t.TempDir()
	au := `{"ts":"2026-11-22T10:00:00Z","event":"tagged","version":"9.9.9-rc.1","stage":"Tag","from":"Sign","user":"sign","machine":"tagged"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(au), 0o600); err != nil {
		t.Fatal(err)
	}
	l := ReadLocal(dir)
	if len(l.Events) != 1 || l.Events[0].Name != "tagged" || l.Events[0].Stage != "Tag" || l.Events[0].From != "Sign" {
		t.Fatalf("%+v", l.Events)
	}
}

// A reason of several lines (what Run throws: the command output) keeps its lines apart, and one hidden line hides only itself.
func TestReasonTextWithSeveralLines(t *testing.T) {
	in := "git fetch failed: fatal: unable to reach host\r\nremote: Support for password authentication was removed.\nAuthorization: token " + fakePass + "\nhint: run gh auth refresh\n" +
		"-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAKHBfpegPjMCMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnVu\n-----END CERTIFICATE-----\nthe real cause is here"
	got := redactText(in, nil)
	for _, want := range []string{"fatal: unable to reach host remote:", "gh auth refresh", "the real cause is here"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q in %q", want, got)
		}
	}
	for _, bad := range []string{fakePass, "MIIBkTCB"} {
		if strings.Contains(got, bad) {
			t.Errorf("still holds a hidden value in %q", got)
		}
	}
}

// A secret that sits over the 300 character limit must be hidden before the text is cut, not cut into a readable half.
func TestReasonSecretOverTheLengthLimitIsNotLeftHalfShowing(t *testing.T) {
	dir := t.TempDir()
	reason := strings.Repeat("x", maxReason-15) + " " + fakeToken
	au := `{"ts":"2026-11-22T10:00:00Z","event":"stopped","version":"9.9.9-rc.1","reason":"` + reason + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(au), 0o600); err != nil {
		t.Fatal(err)
	}
	l := ReadLocal(dir)
	if len(l.Events) != 1 {
		t.Fatalf("%d events", len(l.Events))
	}
	if strings.Contains(l.Events[0].Reason, "ghp_") {
		t.Errorf("part of the token shows: %q", l.Events[0].Reason[len(l.Events[0].Reason)-30:])
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

// REL-4: shapes the Redactor missed, and ordinary lines it must leave alone.
func TestRedactorMoreSecretShapes(t *testing.T) {
	r := func() *Redactor { return NewRedactor() }
	for in, bad := range map[string]string{
		"sigtool sign --passphrase " + fakePass + " manifest.json": fakePass,
		"--token " + fakePass + " at the start of a line":          fakePass,
		"tool --pin=4321 run":                                       "4321",
		"set SIGN_PASS=" + fakePass:                                 fakePass,
		"UNRAVIS_RELEASE_NONCE=" + fakePass:                         fakePass,
		"PROTON_PASS_PIN=4321":                                      "4321",
		`{"key_seed":"` + fakePass + `"}`:                           fakePass,
		"curl -u bob:" + fakePass + " https://x.example":            fakePass,
		"curl --user bob:" + fakePass + " https://x.example":        fakePass,
		"git clone https://bob:" + fakePass + "@host.example/r.git": fakePass,
	} {
		if got := r().Line(in); strings.Contains(got, bad) {
			t.Errorf("%q -> %q still holds the value", in, got)
		}
	}
	for _, ok := range []string{
		"git clone https://github.com/abhinavjp/unravis.git",
		"shipping: 3 files, mapping ok, ping 12ms",
		"go test -count=1 ./...",
		"go get -u ./...",
		"git add -u .",
		"sort -u names.txt",
		"python -u build.py",
		"--- FAIL: TestPassword/seed=42 (0.00s)",
		"jest Seed: 1234567 (use --seed to repeat)",
		"TESTS_PASS: 120 of 120",
		"pytest --passes 3 suite",
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
	write := func(body string) { writeGz(t, path, body) }
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

func writeGz(t *testing.T, path, body string) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	_, _ = gz.Write([]byte(body))
	_ = gz.Close()
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Least recently used: a log that keeps being asked for stays, even when many others are read in between.
func TestReadLogCacheKeepsTheLogThatIsUsed(t *testing.T) {
	dir := t.TempDir()
	ld := filepath.Join(dir, logsDir)
	if err := os.MkdirAll(ld, 0o700); err != nil {
		t.Fatal(err)
	}
	main := "ci-v9.9.9-rc.1-ci-1.log.gz"
	writeGz(t, filepath.Join(ld, main), "job\tstep\tone\n")
	first, _, _ := ReadLog(dir, main, nil)
	for i := 0; i < logCache.maxItems+5; i++ { // more logs than the cache holds
		name := "ci-v9.9.9-rc.1-ci-" + strconv.Itoa(100+i) + ".log.gz"
		writeGz(t, filepath.Join(ld, name), "job\tstep\tx\n")
		_, _, _ = ReadLog(dir, name, nil)
		if again, _, _ := ReadLog(dir, main, nil); &again[0] != &first[0] { // the page keeps asking for its log
			t.Fatalf("the log in use was dropped after %d other logs", i+1)
		}
	}
}

// A slow read must not hold up a quick one, and two callers for the same key share one read.
func TestMemoSlowLoadDoesNotBlockOthersAndSameKeyIsShared(t *testing.T) {
	var m memo[int]
	weigh := func(int) int { return 1 }
	m.maxItems, m.maxWeight = 10, 10
	slow := logKey{path: "slow"}
	release := make(chan struct{})
	started := make(chan struct{})
	var loads int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ { // two callers for the slow key
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.get(slow, true, weigh, func() (int, error) {
				atomic.AddInt32(&loads, 1)
				close(started)
				<-release
				return 7, nil
			})
			if err != nil || v != 7 {
				t.Errorf("slow key: %v %v", v, err)
			}
		}()
	}
	<-started
	done := make(chan int)
	go func() { // another key while the slow one is still being made
		v, _ := m.get(logKey{path: "quick"}, false, weigh, func() (int, error) { return 1, nil })
		done <- v
	}()
	select {
	case v := <-done:
		if v != 1 {
			t.Errorf("quick key = %d", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a quick read waited for the slow one")
	}
	close(release)
	wg.Wait()
	if n := atomic.LoadInt32(&loads); n != 1 {
		t.Errorf("the slow key was made %d times, want 1", n)
	}
}

// The dashboard polls /api/release every 5 s. With a big run log the second poll (file unchanged) must come from the kept lines: the
// first poll reads and redacts the log, the next ones do not (measured before the fix: about 3.5 s of work per poll on 20 000 lines).
func TestReleasePollsReuseTheReadLog(t *testing.T) {
	d := newDash(t)
	ld := filepath.Join(d.DataDir, "logs")
	var sb strings.Builder
	sb.WriteString("2026-11-21T09:59:00Z Pinned commit 0123456\n2026-11-21T10:00:01Z [1/7] Preflight\n")
	for i := 0; i < 6000; i++ {
		sb.WriteString("2026-11-21T10:00:30Z Preflight line " + strconv.Itoa(i) + " token=abc passphrase: not-a-secret path C:\\Users\\bob\\x\n")
	}
	if err := os.WriteFile(filepath.Join(ld, "run-20261121T095900Z.log"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	t0 := time.Now()
	if _, err := d.Release("0.1.0-rc.55", now); err != nil {
		t.Fatal(err)
	}
	first := time.Since(t0)
	t0 = time.Now()
	for i := 0; i < 5; i++ {
		if _, err := d.Release("0.1.0-rc.55", now); err != nil {
			t.Fatal(err)
		}
	}
	five := time.Since(t0)
	t.Logf("first poll %v, five more %v", first, five)
	if first > 300*time.Millisecond && five > first {
		t.Errorf("five more polls took %v, more than the first one (%v): the log is read again", five, first)
	}
}

// A log that is still being written gets a new key at every poll: the old answers for the same file must go, or a live release
// pushes the CI log out of the cache.
func TestMemoReplacesTheOlderAnswerOfTheSameFile(t *testing.T) {
	var m memo[int]
	m.maxItems, m.maxWeight = 10, 1000
	weigh := func(int) int { return 1 }
	ci := logKey{path: "ci", size: 1, mod: 1}
	if _, err := m.get(ci, true, weigh, func() (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ { // fifty polls of a growing run log
		if _, err := m.get(logKey{path: "run", size: int64(i), mod: int64(i)}, false, weigh, func() (int, error) { return i, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.items) != 2 || m.weight != 2 {
		t.Fatalf("%d entries, weight %d: want the CI log and the latest run log only", len(m.items), m.weight)
	}
	calls := 0
	_, _ = m.get(ci, true, weigh, func() (int, error) { calls++; return 1, nil })
	if calls != 0 {
		t.Error("the CI log was pushed out by the run log's polls")
	}
}

// A panic while a log is read must not wedge the cache: the caller gets an error, a waiter is woken and the read slots come back.
func TestMemoPanicInLoadDoesNotWedgeTheCache(t *testing.T) {
	var m memo[int]
	m.maxItems, m.maxWeight = 10, 10
	weigh := func(int) int { return 1 }
	for i := 0; i < 2*cap(bigRead)+2; i++ { // more panics than there are read slots
		_, err := m.get(logKey{path: "bad", size: int64(i)}, true, weigh, func() (int, error) { panic("boom") })
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("panic %d came back as %v", i, err)
		}
	}
	done := make(chan int)
	go func() {
		v, _ := m.get(logKey{path: "good"}, true, weigh, func() (int, error) { return 5, nil })
		done <- v
	}()
	select {
	case v := <-done:
		if v != 5 {
			t.Errorf("good read = %d", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read hangs after panics: the slots were not given back")
	}
	if len(m.flights) != 0 {
		t.Errorf("%d flights left behind", len(m.flights))
	}
}

// A small read (not a CI log) never waits for the big read slots.
func TestMemoSmallReadsDoNotTakeABigReadSlot(t *testing.T) {
	var m memo[int]
	m.maxItems, m.maxWeight = 10, 10
	weigh := func(int) int { return 1 }
	for i := 0; i < cap(bigRead); i++ {
		bigRead <- struct{}{} // every big slot is busy
	}
	defer func() {
		for i := 0; i < cap(bigRead); i++ {
			<-bigRead
		}
	}()
	done := make(chan struct{})
	go func() {
		_, _ = m.get(logKey{path: "small"}, false, weigh, func() (int, error) { return 1, nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a small read waited for a big read slot")
	}
}

func TestRedactorQuotedFlagValueAndUrlPassword(t *testing.T) {
	r := func() *Redactor { return NewRedactor() }
	for in, bad := range map[string]string{
		`tool --password "two words here" run`:   "words",
		`tool --token 'it is secret' run`:        "secret",
		"git clone https://u:p@ss@host.example/": "ss@",
		"MYSQL_PASSWD=" + fakePass:               fakePass,
	} {
		if got := r().Line(in); strings.Contains(got, bad) {
			t.Errorf("%q -> %q still holds %q", in, got, bad)
		}
	}
}

// A lone CR (progress output) separates lines, and a key block is one marker.
func TestRedactTextLoneCRAndKeyBlockMarker(t *testing.T) {
	got := redactText("step one\rstep two\r\n-----BEGIN PRIVATE KEY-----\nMIIBkTCB\nAAAA\n-----END PRIVATE KEY-----\nafter", nil)
	if got != "step one step two <private key hidden> after" {
		t.Errorf("%q", got)
	}
}
