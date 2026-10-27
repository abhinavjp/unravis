package blackbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordFolder runs the real recorder on a fake clock for n ticks of 15 s and
// returns the rig; its folder is a real recordings folder.
func recordFolder(t *testing.T, n int) *rig {
	t.Helper()
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	for i := 0; i < n; i++ {
		g.tick(15 * time.Second)
	}
	return g
}

func folderSource(t *testing.T, dir string) *FolderSource {
	t.Helper()
	loc, err := ParseLocation(dir)
	if err != nil {
		t.Fatal(err)
	}
	return NewFolderSource(loc)
}

func TestFolderReadsManifestAndSamples(t *testing.T) {
	g := recordFolder(t, 40)
	src := folderSource(t, g.s.Dir())
	ctx := context.Background()
	mr, err := src.Manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mr.Manifest.Name != "box" || mr.Manifest.SampleCount() != 41 || !mr.ServerTime.IsZero() {
		t.Fatalf("manifest %+v servertime %v", mr.Manifest, mr.ServerTime)
	}
	got, rep, err := ReadSamples(ctx, src, mr.Manifest, t0, t0.Add(time.Hour))
	if err != nil || rep.Incomplete() || len(rep.Torn) != 0 {
		t.Fatalf("err %v report %+v", err, rep)
	}
	if len(got) != 41 || !got[0].T.Equal(t0) || !got[40].T.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("got %d samples, first %v", len(got), got[0].T)
	}
	if st, err := src.Status(ctx); st != nil || err != nil {
		t.Fatalf("a folder has no status: %v %v", st, err)
	}
}

func TestFolderReadsOnlyFilesThatCanHoldTheWindow(t *testing.T) {
	g := recordFolder(t, 300) // 75 minutes: two hourly files
	src := folderSource(t, g.s.Dir())
	mr, _ := src.Manifest(context.Background())
	if len(mr.Manifest.Files) != 2 {
		t.Fatalf("files %d", len(mr.Manifest.Files))
	}
	got, rep, err := ReadSamples(context.Background(), src, mr.Manifest, t0.Add(70*time.Minute), t0.Add(75*time.Minute))
	if err != nil || rep.Files != 1 {
		t.Fatalf("err %v report %+v", err, rep)
	}
	if len(got) == 0 || got[0].T.Before(t0.Add(70*time.Minute)) {
		t.Fatalf("got %d first %v", len(got), got[0].T)
	}
}

func TestFolderNotFoundAndEmpty(t *testing.T) {
	src := folderSource(t, filepath.Join(t.TempDir(), "nothing-here"))
	_, err := src.Manifest(context.Background())
	if KindOf(err) != ReadNotFound {
		t.Fatalf("err = %v", err)
	}
	empty := folderSource(t, t.TempDir())
	_, err = empty.Manifest(context.Background())
	if KindOf(err) != ReadNotFound || !strings.Contains(err.Error(), "No Blackbox") {
		t.Fatalf("err = %v", err)
	}
}

func TestFolderBadAndNewerManifest(t *testing.T) {
	dir := t.TempDir()
	src := folderSource(t, dir)
	put := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(`{"format":1,"name":`) // half written
	if _, err := src.Manifest(context.Background()); KindOf(err) != ReadBadData {
		t.Fatalf("half manifest: %v", err)
	}
	put(`{"hello":"world"}`)
	if _, err := src.Manifest(context.Background()); KindOf(err) != ReadBadData {
		t.Fatalf("not a manifest: %v", err)
	}
	put(`{"format":99,"name":"x"}`)
	_, err := src.Manifest(context.Background())
	if KindOf(err) != ReadNewerFormat || !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("newer: %v", err)
	}
}

func TestOpenFileRefusesNamesThatAreNotRecordings(t *testing.T) {
	src := folderSource(t, t.TempDir())
	for _, n := range []string{"manifest.json", `..\x`, "../x", "bb-20261027-10.ndjson.exe", "blackbox.json"} {
		if _, err := src.OpenFile(context.Background(), n); KindOf(err) != ReadBadData {
			t.Errorf("%q: %v", n, err)
		}
	}
}

// dropAccess fails the way a dropped network share does: the open works, then the
// read stops with an error that is not a plain "not found".
type dropAccess struct {
	inner FileAccess
	after int  // bytes served before the drop
	drops bool // when false, behaves as the inner access
	only  string
}

type dropReader struct {
	r    io.ReadCloser
	left int
}

func (d *dropReader) Read(p []byte) (int, error) {
	if d.left <= 0 {
		return 0, errors.New("The specified network name is no longer available.")
	}
	if len(p) > d.left {
		p = p[:d.left]
	}
	n, err := d.r.Read(p)
	d.left -= n
	return n, err
}
func (d *dropReader) Close() error { return d.r.Close() }

func (a dropAccess) ReadFile(name string) ([]byte, error) { return a.inner.ReadFile(name) }
func (a dropAccess) Open(name string) (io.ReadCloser, error) {
	rc, err := a.inner.Open(name)
	if err != nil || !a.drops || (a.only != "" && name != a.only) {
		return rc, err
	}
	return &dropReader{r: rc, left: a.after}, nil
}

// A share that drops in the middle of an hour keeps what was read before the drop,
// reports the file as dropped, and the next file is still tried (clumsy case 2).
func TestShareDroppingMidReadKeepsEarlySamplesAndFlagsIncomplete(t *testing.T) {
	g := recordFolder(t, 300)
	loc, _ := ParseLocation(`\\web-b\bb`)
	first := FileNameFor(t0)
	src := &FolderSource{Loc: loc, Access: dropAccess{inner: DirAccess{Dir: g.s.Dir()}, after: 4000, drops: true, only: first}}
	ctx := context.Background()
	mr, err := src.Manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, rep, err := ReadSamples(ctx, src, mr.Manifest, t0, t0.Add(80*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Dropped) != 1 || rep.Dropped[0] != first || !rep.Incomplete() {
		t.Fatalf("report %+v", rep)
	}
	if len(got) == 0 || len(got) >= 240+30 {
		t.Fatalf("got %d samples: want some of hour 1 and all of hour 2", len(got))
	}
	// The second hour was read in full: the last sample is there.
	if !got[len(got)-1].T.Equal(t0.Add(75 * time.Minute)) {
		t.Fatalf("last %v", got[len(got)-1].T)
	}
	// The early samples are real, never padded with zero: every sample has a CPU value.
	for _, s := range got {
		if s.CPU == nil {
			t.Fatalf("sample %v has no cpu", s.T)
		}
	}
	// The state says Incomplete, Partly ready.
	r := Reading{Manifest: mr.Manifest, ReadAt: t0.Add(75 * time.Minute)}
	si := DeriveState(StateInput{Placed: true, Reading: &r, Report: &rep})
	if si.State != SIncomplete || si.Readiness != WordPartlyReady {
		t.Fatalf("state %+v", si)
	}
}

func TestShareThatIsGoneIsUnreachableNotNotFound(t *testing.T) {
	loc, _ := ParseLocation(`\\web-b\bb`)
	src := &FolderSource{Loc: loc, Access: failAccess{err: errors.New("The network path was not found.")}}
	_, err := src.Manifest(context.Background())
	if KindOf(err) != ReadUnreachable || !strings.Contains(err.Error(), "share") {
		t.Fatalf("err = %v", err)
	}
	src.Access = failAccess{err: os.ErrPermission}
	_, err = src.Manifest(context.Background())
	if KindOf(err) != ReadUnreachable || !strings.Contains(err.Error(), "read access") {
		t.Fatalf("err = %v", err)
	}
}

type failAccess struct{ err error }

func (f failAccess) ReadFile(string) ([]byte, error)    { return nil, f.err }
func (f failAccess) Open(string) (io.ReadCloser, error) { return nil, f.err }

func TestMissingFileListedInManifestIsReportedMissing(t *testing.T) {
	g := recordFolder(t, 300)
	src := folderSource(t, g.s.Dir())
	mr, _ := src.Manifest(context.Background())
	if err := os.Remove(filepath.Join(g.s.Dir(), FileNameFor(t0))); err != nil {
		t.Fatal(err)
	}
	got, rep, err := ReadSamples(context.Background(), src, mr.Manifest, t0, t0.Add(80*time.Minute))
	if err != nil || len(rep.Missing) != 1 || !rep.Incomplete() {
		t.Fatalf("err %v report %+v", err, rep)
	}
	if len(got) == 0 || got[0].T.Before(t0.Add(time.Hour)) {
		t.Fatalf("only the second hour is left: %v", got[0].T)
	}
}

func TestTornLastLineIsReportedAndKeptOut(t *testing.T) {
	g := recordFolder(t, 8)
	name := FileNameFor(t0)
	p := filepath.Join(g.s.Dir(), name)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":1,"run":"run1","seq":99,"t":"2026-10-27T10:`)
	f.Close()
	src := folderSource(t, g.s.Dir())
	mr, _ := src.Manifest(context.Background())
	got, rep, err := ReadSamples(context.Background(), src, mr.Manifest, t0, t0.Add(time.Hour))
	if err != nil || len(rep.Torn) != 1 || rep.Incomplete() {
		t.Fatalf("err %v report %+v", err, rep)
	}
	if len(got) != 9 {
		t.Fatalf("got %d", len(got))
	}
}

func TestSamplesFromANewerRecorderAreFlagged(t *testing.T) {
	dir := t.TempDir()
	name := FileNameFor(t0)
	line := `{"v":9,"run":"r","seq":1,"t":"2026-10-27T10:00:00Z","cpu":5}` + "\n" +
		`{"v":1,"run":"r","seq":2,"t":"2026-10-27T10:00:15Z","cpu":6}` + "\n"
	os.WriteFile(filepath.Join(dir, name), []byte(line), 0o644)
	src := folderSource(t, dir)
	m := Manifest{Format: 1, Files: []FileEntry{{Name: name, Hour: t0}}}
	got, rep, _ := ReadSamples(context.Background(), src, m, t0, t0.Add(time.Hour))
	if !rep.Newer || len(got) != 1 {
		t.Fatalf("got %d report %+v", len(got), rep)
	}
}

/* ------------------------------------------------------------------ URL */

func httpSource(t *testing.T, ts *httptest.Server, key string) *HTTPSource {
	t.Helper()
	loc, err := ParseLocation(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return NewHTTPSource(loc, key, nil)
}

func TestURLReadsWithTheRightKey(t *testing.T) {
	g := newSrvRig(t)
	src := httpSource(t, g.ts, g.key)
	ctx := context.Background()
	rd, err := ProbeSource(ctx, src, g.clk.now)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Manifest.Name != "box" || rd.Status == nil || rd.Status.State != StateRunning {
		t.Fatalf("%+v", rd)
	}
	if !rd.ServerTime.Equal(g.clk.t) {
		t.Fatalf("server time %v want %v", rd.ServerTime, g.clk.t)
	}
	got, rep, err := ReadSamples(ctx, src, rd.Manifest, t0, t0.Add(time.Hour))
	if err != nil || rep.Incomplete() || len(got) != 2 {
		t.Fatalf("err %v got %d report %+v", err, len(got), rep)
	}
}

// A URL with the wrong key is refused with plain words and the key is not in them
// (clumsy case 3).
func TestURLWithWrongKeyIsRefusedAndNeverEchoed(t *testing.T) {
	g := newSrvRig(t)
	wrong := "ubk-WRONGWRONGWRONGKEY"
	src := httpSource(t, g.ts, wrong)
	_, err := src.Manifest(context.Background())
	if KindOf(err) != ReadKeyRefused {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(err.Error(), g.key) {
		t.Fatalf("the key is in the message: %v", err)
	}
	si := DeriveState(StateInput{Placed: true, Err: err})
	if si.State != SUnreachable || si.Reason != "key" || si.Readiness != WordCannotTestYet {
		t.Fatalf("state %+v", si)
	}
	if strings.Contains(si.Detail, wrong) {
		t.Fatal("key in state detail")
	}
	// No key at all is refused too.
	_, err = httpSource(t, g.ts, "").Manifest(context.Background())
	if KindOf(err) != ReadKeyRefused {
		t.Fatalf("no key: %v", err)
	}
}

func TestURLThrottledAfterRepeatedWrongKeys(t *testing.T) {
	g := newSrvRig(t)
	src := httpSource(t, g.ts, "ubk-nope")
	var err error
	for i := 0; i < maxFailures+2; i++ {
		_, err = src.Manifest(context.Background())
	}
	if KindOf(err) != ReadThrottled {
		t.Fatalf("err = %v", err)
	}
	// Even the right key waits out the throttle: the state says to wait.
	si := DeriveState(StateInput{Placed: true, Err: err})
	if si.State != SUnreachable || si.Reason != "wait" {
		t.Fatalf("state %+v", si)
	}
}

func TestURLBusyAndOtherAnswers(t *testing.T) {
	codes := []struct {
		code int
		kind ReadKind
	}{{503, ReadBusy}, {404, ReadNotFound}, {500, ReadNotFound}, {401, ReadKeyRefused}, {429, ReadThrottled}}
	for _, c := range codes {
		c := c
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.code) }))
		_, err := httpSource(t, ts, "ubk-k").Manifest(context.Background())
		ts.Close()
		if KindOf(err) != c.kind {
			t.Errorf("%d: kind %v err %v", c.code, KindOf(err), err)
		}
	}
}

func TestURLSendsTheKeyOnlyInTheHeader(t *testing.T) {
	var gotURL, gotHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotHeader = r.Header.Get(KeyHeader)
		w.WriteHeader(404)
	}))
	defer ts.Close()
	httpSource(t, ts, "ubk-SECRET").Manifest(context.Background())
	if gotHeader != "ubk-SECRET" || strings.Contains(gotURL, "SECRET") {
		t.Fatalf("url %q header %q", gotURL, gotHeader)
	}
}

func TestURLNeverFollowsARedirect(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Write([]byte(`{}`))
	}))
	defer other.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/manifest", http.StatusFound)
	}))
	defer ts.Close()
	_, err := httpSource(t, ts, "ubk-SECRET").Manifest(context.Background())
	if hit {
		t.Fatal("the redirect was followed: the key could have gone to another address")
	}
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestURLNothingListeningIsUnreachableAndSilentAboutTheKey(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	loc, _ := ParseLocation(ts.URL)
	ts.Close() // the port is now closed
	_, err := NewHTTPSource(loc, "ubk-SECRET", nil).Manifest(context.Background())
	if KindOf(err) != ReadUnreachable {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), loc.Host) {
		t.Fatalf("message leaks: %v", err)
	}
	si := DeriveState(StateInput{Placed: true, Err: err})
	if si.State != SUnreachable || si.Reason != "timeout" {
		t.Fatalf("state %+v", si)
	}
}

func TestURLThatIsNotABlackboxIsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>hello</html>")) }))
	defer ts.Close()
	_, err := httpSource(t, ts, "k").Manifest(context.Background())
	if KindOf(err) != ReadNotFound {
		t.Fatalf("err = %v", err)
	}
	_, err = httpSource(t, ts, "k").Status(context.Background())
	if KindOf(err) != ReadNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestURLNewerFormatIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"format":7,"manifest":{"format":7}}`))
	}))
	defer ts.Close()
	_, err := httpSource(t, ts, "k").Manifest(context.Background())
	if KindOf(err) != ReadNewerFormat {
		t.Fatalf("err = %v", err)
	}
	si := DeriveState(StateInput{Placed: true, Err: err})
	if si.State != SUnreachable || si.Reason != "newer-format" {
		t.Fatalf("state %+v", si)
	}
}

func TestURLTimeoutIsUnreachable(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer ts.Close()
	defer close(block)
	loc, _ := ParseLocation(ts.URL)
	src := NewHTTPSource(loc, "k", &http.Client{Timeout: 100 * time.Millisecond})
	_, err := src.Manifest(context.Background())
	if KindOf(err) != ReadUnreachable || !strings.Contains(err.Error(), "in time") {
		t.Fatalf("err = %v", err)
	}
}

func TestURLConnectionCutMidFileKeepsEarlySamples(t *testing.T) {
	g := newSrvRig(t)
	for i := 0; i < 20; i++ {
		g.tick(15 * time.Second)
	}
	first := FileNameFor(t0)
	// A front that serves the real recorder but cuts the file download part way.
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			rec := httptest.NewRecorder()
			g.srv.Handler().ServeHTTP(rec, r)
			body := rec.Body.Bytes()
			w.Header().Set("Content-Length", "999999")
			w.Write(body[:len(body)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			hj, ok := w.(http.Hijacker)
			if ok {
				c, _, _ := hj.Hijack()
				c.Close()
			}
			return
		}
		g.srv.Handler().ServeHTTP(w, r)
	}))
	defer front.Close()
	src := httpSource(t, front, g.key)
	ctx := context.Background()
	mr, err := src.Manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, rep, err := ReadSamples(ctx, src, mr.Manifest, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Dropped) != 1 || rep.Dropped[0] != first || len(got) == 0 || len(got) >= 22 {
		t.Fatalf("got %d report %+v", len(got), rep)
	}
}

// hangAccess blocks the way a dead share does: the call does not come back until
// release is closed.
type hangAccess struct{ release chan struct{} }

func (h hangAccess) ReadFile(string) ([]byte, error) { <-h.release; return nil, errors.New("late") }
func (h hangAccess) Open(string) (io.ReadCloser, error) {
	<-h.release
	return nil, errors.New("late")
}

// hangRead opens fine, then the first read blocks.
type hangReadAccess struct{ release chan struct{} }

func (h hangReadAccess) ReadFile(string) ([]byte, error) { return nil, errors.New("unused") }
func (h hangReadAccess) Open(string) (io.ReadCloser, error) {
	return blockedReader{release: h.release}, nil
}

type blockedReader struct{ release chan struct{} }

func (b blockedReader) Read(p []byte) (int, error) { <-b.release; return 0, io.EOF }
func (b blockedReader) Close() error              { return nil }

// A dead share must not hold a read past its time limit (F2): the call comes back
// as Unreachable when the context ends, whatever the file system is doing.
func TestDeadShareHonoursTheContextOnManifestOpenAndRead(t *testing.T) {
	loc, _ := ParseLocation(`\gone\share`)
	rel := make(chan struct{})
	defer close(rel)
	within := func(name string, f func(ctx context.Context) error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- f(ctx) }()
		select {
		case err := <-done:
			if KindOf(err) != ReadUnreachable {
				t.Errorf("%s: kind %v err %v", name, KindOf(err), err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: hung past the context", name)
		}
	}
	hang := &FolderSource{Loc: loc, Access: hangAccess{release: rel}}
	within("manifest", func(ctx context.Context) error { _, err := hang.Manifest(ctx); return err })
	within("open", func(ctx context.Context) error {
		rc, err := hang.OpenFile(ctx, FileNameFor(t0))
		if rc != nil {
			rc.Close()
		}
		return err
	})
	slow := &FolderSource{Loc: loc, Access: hangReadAccess{release: rel}}
	within("read", func(ctx context.Context) error {
		rc, err := slow.OpenFile(ctx, FileNameFor(t0))
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.ReadAll(rc)
		return err
	})
}
