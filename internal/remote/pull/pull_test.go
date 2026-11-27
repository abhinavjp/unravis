package pull

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/remote/transport"
)

type fakeFile struct {
	data  []byte
	mtime time.Time
}

type fakeT struct {
	mu        sync.Mutex
	files     map[string]*fakeFile
	failAfter int64 // when > 0, the next Open from offset 0 delivers this many bytes then errors, once
	opens     []int64
	badDigest bool
	afterRead func() // runs after a stream is fully delivered
}

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func newFake(files map[string]string) *fakeT {
	f := &fakeT{files: map[string]*fakeFile{}}
	for n, c := range files {
		f.files[n] = &fakeFile{data: []byte(c), mtime: t0}
	}
	return f
}

func (f *fakeT) Probe(context.Context) (transport.HostInfo, error) { return transport.HostInfo{}, nil }
func (f *fakeT) Close() error                                      { return nil }
func (f *fakeT) List(_ context.Context, dir string) ([]transport.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []transport.FileInfo
	for n, ff := range f.files {
		out = append(out, transport.FileInfo{Name: n, Size: int64(len(ff.data)), MTime: ff.mtime})
	}
	return out, nil
}
func (f *fakeT) Digest(_ context.Context, p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ff := f.files[baseOf(p)]
	if ff == nil {
		return "", errors.New("no such file")
	}
	if f.badDigest {
		return strings.Repeat("0", 64), nil
	}
	s := sha256.Sum256(ff.data)
	return hex.EncodeToString(s[:]), nil
}
func (f *fakeT) Open(ctx context.Context, p string, off int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ff := f.files[baseOf(p)]
	if ff == nil {
		return nil, errors.New("no such file")
	}
	f.opens = append(f.opens, off)
	data := ff.data[off:]
	if off == 0 && f.failAfter > 0 {
		n := f.failAfter
		f.failAfter = 0
		return io.NopCloser(io.MultiReader(bytes.NewReader(data[:n]), errReader{})), nil
	}
	after := f.afterRead
	if after != nil {
		return io.NopCloser(io.MultiReader(bytes.NewReader(data), hookReader{after})), nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

type hookReader struct{ f func() }

func (h hookReader) Read([]byte) (int, error) { h.f(); return 0, io.EOF }

func baseOf(p string) string { return p[strings.LastIndexAny(p, `\/`)+1:] }

func run(t *testing.T, f *fakeT, kind, pattern string, lim Limits, job string, dir string) (Result, error) {
	t.Helper()
	req := Request{Kind: kind, Dir: "/var/log/x", Pattern: pattern}
	plan, err := BuildPlan(context.Background(), f, req, lim)
	if err != nil {
		t.Fatal(err)
	}
	return Run(context.Background(), f, req, plan, Options{StagingDir: dir, JobID: job, Server: "srv", Limits: lim, Free: func(string) (int64, error) { return 1 << 40, nil }})
}

func TestPullScrubsHashesAndLeavesNoRawSecretOnDisk(t *testing.T) {
	f := newFake(map[string]string{
		"a.log": "ok line\npassword=hunter2 here\nconnect mongodb://u:pw@h/db now\nlast line no newline",
		"b.log": "AUTH s3cretpw\r\nfine\r\n",
	})
	dir := t.TempDir()
	res, err := run(t, f, "redis", "", Limits{}, "job1", dir)
	if err != nil || !res.OK() {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, fr := range res.Files {
		if !fr.Verified || fr.Status != statusDone {
			t.Errorf("%+v", fr)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "job1", "a.log"))
	want := "ok line\n[removed]\nconnect [removed] now\nlast line no newline\n"
	if string(got) != want {
		t.Errorf("a.log = %q", got)
	}
	if res.Files[0].Scrubbed != 2 {
		t.Errorf("scrubbed = %d", res.Files[0].Scrubbed)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "job1", "b.log"))
	if string(b) != "[removed]\nfine\n" {
		t.Errorf("b.log = %q", b)
	}
	filepath.Walk(dir, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			c, _ := os.ReadFile(p)
			for _, s := range []string{"hunter2", "pw@h", "s3cretpw"} {
				if bytes.Contains(c, []byte(s)) {
					t.Errorf("%s holds %q", p, s)
				}
			}
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(dir, "job1", RecordName)); err != nil {
		t.Error("no manifest")
	}
}

func TestResumeAfterADroppedConnection(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("line number " + string(rune('A'+i%26)) + " token=abc123\n")
	}
	content := sb.String()
	for _, tc := range []struct {
		name  string
		after int64
	}{{"on a line boundary", int64(len("line number A token=abc123\n")) * 10}, {"mid line", 205}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(map[string]string{"big.log": content})
			f.failAfter = tc.after
			dir := t.TempDir()
			res, err := run(t, f, "applog", "", Limits{}, "j", dir)
			if err != nil || !res.Interrupted || res.Files[0].Status != statusInterrupt {
				t.Fatalf("first run: %+v %v", res, err)
			}
			res, err = run(t, f, "applog", "", Limits{}, "j", dir)
			if err != nil || !res.OK() || !res.Files[0].Verified {
				t.Fatalf("second run: %+v %v", res, err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "j", "big.log"))
			if strings.Count(string(got), "[removed]") != 50 || strings.Count(string(got), "\n") != 50 {
				t.Errorf("output wrong: %d lines", strings.Count(string(got), "\n"))
			}
			if tc.after%27 == 0 && f.opens[len(f.opens)-1] == 0 {
				t.Errorf("a boundary drop should resume from an offset, opens=%v", f.opens)
			}
		})
	}
}

func TestADoneFileIsNotCopiedAgain(t *testing.T) {
	f := newFake(map[string]string{"a.log": "x\n"})
	dir := t.TempDir()
	if res, err := run(t, f, "applog", "", Limits{}, "j", dir); err != nil || !res.OK() {
		t.Fatal(res, err)
	}
	n := len(f.opens)
	if res, err := run(t, f, "applog", "", Limits{}, "j", dir); err != nil || !res.OK() || len(f.opens) != n {
		t.Fatalf("second run opened again: %v", f.opens)
	}
}

func TestAStableFileWithTheWrongDigestIsThrownAway(t *testing.T) {
	f := newFake(map[string]string{"a.log": "hello\n"})
	f.badDigest = true
	dir := t.TempDir()
	res, _ := run(t, f, "applog", "", Limits{}, "j", dir)
	if res.Files[0].Status != statusFailed || !strings.Contains(res.Files[0].Note, "does not match") {
		t.Fatalf("%+v", res.Files[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "j", "a.log")); err == nil {
		t.Error("a bad copy was kept")
	}
}

func TestAFileThatChangedWhileCopyingIsKeptAsAnUncheckedSnapshot(t *testing.T) {
	f := newFake(map[string]string{"a.log": "one\n"})
	f.afterRead = func() {
		f.mu.Lock()
		f.files["a.log"].data = append(f.files["a.log"].data, "two\n"...)
		f.files["a.log"].mtime = t0.Add(time.Minute)
		f.mu.Unlock()
	}
	dir := t.TempDir()
	res, _ := run(t, f, "applog", "", Limits{}, "j", dir)
	fr := res.Files[0]
	if fr.Status != statusDone || fr.Verified || !fr.Changed {
		t.Fatalf("%+v", fr)
	}
}

func TestPlanLimits(t *testing.T) {
	f := newFake(map[string]string{"big.log": strings.Repeat("x", 100), "ok1.log": "aaaa", "ok2.log": "bbbb", "old.log": "zz", "skip.txt": "t"})
	f.files["old.log"].mtime = t0.Add(-48 * time.Hour)
	p, _ := BuildPlan(context.Background(), f, Request{Kind: "applog", Dir: "/var/log/x", Pattern: "*.log", Since: t0.Add(-time.Hour)}, Limits{PerFile: 50})
	names := []string{}
	for _, x := range p.Files {
		names = append(names, x.Name)
	}
	if strings.Join(names, ",") != "ok1.log,ok2.log" || len(p.Skipped) != 2 || p.Problem != "" {
		t.Fatalf("%+v", p)
	}
	p, _ = BuildPlan(context.Background(), f, Request{Kind: "applog", Dir: "/var/log/x", Pattern: "ok*"}, Limits{PerJob: 6})
	if p.Problem == "" {
		t.Error("job size limit not reported")
	}
	if _, err := Run(context.Background(), f, Request{}, p, Options{StagingDir: t.TempDir(), JobID: "j"}); !errors.As(err, &ErrPlanTooLarge{}) {
		t.Errorf("Run ran a refused plan: %v", err)
	}
	if _, err := BuildPlan(context.Background(), f, Request{Dir: "/var/log/x", Pattern: "[x"}, Limits{}); err == nil {
		t.Error("bad pattern accepted")
	}
	p, _ = BuildPlan(context.Background(), f, Request{Dir: "/var/log/x"}, Limits{MaxFiles: 2})
	if p.Problem == "" {
		t.Error("file count limit not reported")
	}
}

func TestPlanPatternAlternation(t *testing.T) {
	f := newFake(map[string]string{"manifest.json": "{}", "bb-1.ndjson": "a", "BB-2.NDJSON": "b", "other.log": "c"})
	p, err := BuildPlan(context.Background(), f, Request{Dir: "/d", Pattern: " manifest.json | bb-*.ndjson "}, Limits{})
	if err != nil || len(p.Files) != 3 {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := BuildPlan(context.Background(), f, Request{Dir: "/d", Pattern: "ok|[x"}, Limits{}); err == nil {
		t.Error("a bad second pattern was accepted")
	}
	if p, _ = BuildPlan(context.Background(), f, Request{Dir: "/d", Pattern: "a*|"}, Limits{}); len(p.Files) != 4 {
		t.Errorf("an empty alternative should match everything: %+v", p)
	}
}

func TestLongLinesBinaryAndDisk(t *testing.T) {
	f := newFake(map[string]string{"long.log": "short\n" + strings.Repeat("y", 5000) + "\nafter\n", "bin.log": "ab\x00cd\n"})
	dir := t.TempDir()
	res, _ := run(t, f, "applog", "", Limits{MaxLine: 1000}, "j", dir)
	var long, bin FileResult
	for _, fr := range res.Files {
		if fr.Name == "long.log" {
			long = fr
		} else {
			bin = fr
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "j", "long.log"))
	if string(got) != "short\nafter\n" || long.Dropped != 1 || !long.Verified {
		t.Errorf("long: %q %+v", got, long)
	}
	if bin.Status != statusFailed || !strings.Contains(bin.Note, "plain text") {
		t.Errorf("bin: %+v", bin)
	}
	req := Request{Kind: "applog", Dir: "/var/log/x"}
	plan, _ := BuildPlan(context.Background(), f, req, Limits{})
	_, err := Run(context.Background(), f, req, plan, Options{StagingDir: t.TempDir(), JobID: "k", Free: func(string) (int64, error) { return 10, nil }})
	if err == nil || !strings.Contains(err.Error(), "free disk space") {
		t.Errorf("disk check: %v", err)
	}
	_, err = Run(context.Background(), f, req, plan, Options{StagingDir: t.TempDir(), JobID: "../x"})
	if err == nil {
		t.Error("bad job id accepted")
	}
}

func TestSafeName(t *testing.T) {
	used := map[string]bool{}
	cases := map[string]string{
		`..\..\evil.log`: "evil.log",
		"a b?.log":       "a_b_.log",
		"CON.log":        "_CON.log",
		"x.part":         "x.part_",
		RecordName:       RecordName + "_",
		"...":            "file",
	}
	for in, want := range cases {
		if got := safeName(in, used); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
	if a, b := safeName("Dup.log", used), safeName("dup.LOG", used); a == b || strings.ToLower(a) == strings.ToLower(b) {
		t.Errorf("duplicates collide: %q %q", a, b)
	}
}

func TestJobLimitStopsAFileThatGrew(t *testing.T) {
	f := newFake(map[string]string{"a.log": strings.Repeat("x\n", 100)})
	req := Request{Kind: "applog", Dir: "/var/log/x"}
	plan, _ := BuildPlan(context.Background(), f, req, Limits{})
	res, _ := Run(context.Background(), f, req, plan, Options{StagingDir: t.TempDir(), JobID: "j", Limits: Limits{PerJob: 50}, Free: func(string) (int64, error) { return 1 << 40, nil }})
	if res.Files[0].Status != statusFailed || !strings.Contains(res.Files[0].Note, "total limit") {
		t.Fatalf("%+v", res.Files[0])
	}
}

func TestCancelledRunIsResumable(t *testing.T) {
	f := newFake(map[string]string{"a.log": "one\ntwo\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := Request{Kind: "applog", Dir: "/var/log/x"}
	plan, _ := BuildPlan(ctx, f, req, Limits{})
	dir := t.TempDir()
	res, err := Run(ctx, f, req, plan, Options{StagingDir: dir, JobID: "j", Free: func(string) (int64, error) { return 1 << 40, nil }})
	if err != nil || !res.Interrupted {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = run(t, f, "applog", "", Limits{}, "j", dir)
	if err != nil || !res.OK() {
		t.Fatalf("%+v %v", res, err)
	}
}
