package blackbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
func (b blockedReader) Close() error               { return nil }

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

// The read key is sent in a header, so a read never goes through an environment
// proxy (F5): the transport has no proxy, whichever client was given.
func TestHTTPReadNeverUsesAProxy(t *testing.T) {
	loc, _ := ParseLocation("web-b:7788")
	withProxy := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	for name, c := range map[string]*http.Client{"default": nil, "given": withProxy} {
		src := NewHTTPSource(loc, "k", c)
		tr, ok := src.Client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("%s: transport is %T", name, src.Client.Transport)
		}
		if tr.Proxy != nil {
			t.Errorf("%s: a proxy is set", name)
		}
	}
	// The caller's own client is left as it was.
	if withProxy.Transport.(*http.Transport).Proxy == nil {
		t.Error("the given client was changed")
	}
}

/* ------------------------------------------- W12-01: coverage honesty */

// Incomplete stays the page-state predicate (a torn tail of a file being
// written is normal); CoverageIncomplete is the lane predicate: every reason
// that leaves time unobserved counts.
func TestReadReportCoverageIncomplete(t *testing.T) {
	clean := ReadReport{Files: 2, Samples: 10}
	if clean.Incomplete() || clean.CoverageIncomplete() {
		t.Fatalf("clean read flagged: %+v", clean)
	}
	for name, rep := range map[string]ReadReport{
		"dropped": {Dropped: []string{"a"}},
		"missing": {Missing: []string{"a"}},
		"newer":   {Newer: true},
		"torn":    {Torn: []string{"a"}},
		"corrupt": {Corrupt: []string{"a"}},
		"capped":  {Capped: true, OmittedFiles: 1},
		"limited": {Limited: true},
	} {
		if !rep.CoverageIncomplete() {
			t.Errorf("%s: CoverageIncomplete false", name)
		}
	}
	// The state contract is unchanged: torn/newer alone are not Incomplete.
	if (ReadReport{Torn: []string{"a"}}).Incomplete() || (ReadReport{Newer: true}).Incomplete() {
		t.Fatal("torn/newer changed the page-state predicate")
	}
}

// mapSource serves fixed files in memory and counts opens.
type mapSource struct {
	files map[string][]byte
	opens *[]string
}

func (m mapSource) Manifest(context.Context) (ManifestRead, error) {
	return ManifestRead{}, nil
}
func (m mapSource) Status(context.Context) (*StatusDoc, error) { return nil, nil }
func (m mapSource) OpenFile(_ context.Context, name string) (io.ReadCloser, error) {
	if m.opens != nil {
		*m.opens = append(*m.opens, name)
	}
	b, ok := m.files[name]
	if !ok {
		return nil, readErr(ReadNotFound, "gone", nil)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func sampleLine(t time.Time, seq uint64) string {
	return fmt.Sprintf(`{"v":1,"run":"r","seq":%d,"t":%q,"cpu":5}`, seq, t.UTC().Format(time.RFC3339Nano)) + "\n"
}

// W12-01: a malformed line in the MIDDLE of a file is corruption, not a clean
// read: the samples around it are kept and the file is reported Corrupt.
func TestReadSamplesCorruptInteriorLineIsReported(t *testing.T) {
	dir := t.TempDir()
	name := FileNameFor(t0)
	var buf strings.Builder
	buf.WriteString(sampleLine(t0, 1))
	buf.WriteString(`{"v":1,"run":"r","seq":2,"t":"2026-10-27T10:`) // cut interior line
	buf.WriteString("\n")
	buf.WriteString(sampleLine(t0.Add(30*time.Second), 3))
	os.WriteFile(filepath.Join(dir, name), []byte(buf.String()), 0o644)
	src := folderSource(t, dir)
	m := Manifest{Format: 1, Files: []FileEntry{{Name: name, Hour: t0}}}
	got, rep, err := ReadSamples(context.Background(), src, m, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(rep.Corrupt) != 1 || rep.Corrupt[0] != name {
		t.Fatalf("got %d report %+v, want the two good samples and one corrupt file", len(got), rep)
	}
	if rep.Incomplete() || !rep.CoverageIncomplete() {
		t.Fatalf("corrupt interior line must be coverage-incomplete, not dropped: %+v", rep)
	}
	if len(rep.Torn) != 0 {
		t.Fatalf("an interior bad line is not a torn tail: %+v", rep)
	}
}

// W12-01: 193 overlapping hourly entries lose the oldest to MaxReadFiles, and
// the read SAYS SO (Capped, OmittedFiles) instead of implying full coverage.
func TestReadSamplesFileCapReportsOmittedCoverage(t *testing.T) {
	files := map[string][]byte{}
	var entries []FileEntry
	var opens []string
	for i := 0; i < MaxReadFiles+1; i++ {
		h := t0.Add(time.Duration(i) * time.Hour)
		name := FileNameFor(h)
		files[name] = []byte(sampleLine(h, uint64(i+1)) + sampleLine(h.Add(30*time.Minute), uint64(i+1)))
		entries = append(entries, FileEntry{Name: name, Hour: h})
	}
	src := mapSource{files: files, opens: &opens}
	m := Manifest{Format: 1, Files: entries}
	got, rep, err := ReadSamples(context.Background(), src, m, t0, t0.Add(time.Duration(MaxReadFiles+1)*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Capped || rep.OmittedFiles != 1 || rep.Files != MaxReadFiles || !rep.CoverageIncomplete() {
		t.Fatalf("cap not reported: %+v", rep)
	}
	if len(opens) != MaxReadFiles {
		t.Fatalf("opened %d files, want %d", len(opens), MaxReadFiles)
	}
	for _, name := range opens {
		if name == FileNameFor(t0) {
			t.Fatal("the oldest file was opened past the cap")
		}
	}
	if len(got) == 0 || got[0].T.Before(t0.Add(time.Hour)) {
		t.Fatalf("oldest hour present or nothing read: %d first %v", len(got), got[0].T)
	}
}

// W12-04: the cumulative byte budget stops the read at limit+1 bytes and marks
// it Limited (incomplete), never a clean EOF.
func TestReadSamplesByteBudgetStopsAtLimitPlusOne(t *testing.T) {
	old := maxReadTotalBytes
	maxReadTotalBytes = 500
	t.Cleanup(func() { maxReadTotalBytes = old })
	name := FileNameFor(t0)
	var buf strings.Builder
	for i := 0; i < 200; i++ {
		buf.WriteString(sampleLine(t0.Add(time.Duration(i)*15*time.Second), uint64(i+1)))
	}
	served := 0
	src := countOpenSource{data: []byte(buf.String()), served: &served}
	m := Manifest{Format: 1, Files: []FileEntry{{Name: name, Hour: t0}}}
	got, rep, err := ReadSamples(context.Background(), src, m, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if served > 501 {
		t.Fatalf("read served %d bytes, want at most limit+1 (501)", served)
	}
	if !rep.Limited || !rep.CoverageIncomplete() || rep.Incomplete() {
		t.Fatalf("budget stop not reported as limited coverage: %+v", rep)
	}
	if len(got) == 0 || len(got) >= 200 {
		t.Fatalf("got %d samples, want a bounded prefix", len(got))
	}
}

// countOpenSource serves one file and counts the bytes actually read from it.
type countOpenSource struct {
	data   []byte
	served *int
}

func (c countOpenSource) Manifest(context.Context) (ManifestRead, error) {
	return ManifestRead{}, nil
}
func (c countOpenSource) Status(context.Context) (*StatusDoc, error) { return nil, nil }
func (c countOpenSource) OpenFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(&countReader{r: bytes.NewReader(c.data), n: c.served}), nil
}

type countReader struct {
	r *bytes.Reader
	n *int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}

// W12-04: the decoded-sample budget stops at limit+1 objects and keeps at most
// the limit, marked Limited.
func TestReadSamplesSampleBudgetStopsAtLimitPlusOne(t *testing.T) {
	old := maxReadTotalSamples
	maxReadTotalSamples = 10
	t.Cleanup(func() { maxReadTotalSamples = old })
	name := FileNameFor(t0)
	var buf strings.Builder
	for i := 0; i < 100; i++ {
		buf.WriteString(sampleLine(t0.Add(time.Duration(i)*15*time.Second), uint64(i+1)))
	}
	src := mapSource{files: map[string][]byte{name: []byte(buf.String())}}
	m := Manifest{Format: 1, Files: []FileEntry{{Name: name, Hour: t0}}}
	got, rep, err := ReadSamples(context.Background(), src, m, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 10 || !rep.Limited || !rep.CoverageIncomplete() {
		t.Fatalf("got %d report %+v, want at most 10 kept and Limited", len(got), rep)
	}
}

// W12-04: cancellation is checked in the decode loop: a large file whose source
// keeps answering after the context ends does not hold the read.
func TestReadSamplesChecksCancellationWhileDecoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	name := FileNameFor(t0)
	var buf strings.Builder
	for i := 0; i < 5000; i++ {
		buf.WriteString(sampleLine(t0.Add(time.Duration(i)*time.Second), uint64(i+1)))
	}
	src := cancelAfterSource{data: []byte(buf.String()), cancel: cancel, after: 4000}
	m := Manifest{Format: 1, Files: []FileEntry{{Name: name, Hour: t0}}}
	done := make(chan struct{})
	var got []Sample
	var rep ReadReport
	var err error
	go func() {
		got, rep, err = ReadSamples(ctx, src, m, t0, t0.Add(2*time.Hour))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("decode loop ignored the cancelled context")
	}
	if err == nil || KindOf(err) != ReadUnreachable {
		t.Fatalf("err %v, want the cancelled read", err)
	}
	if len(got) >= 5000 {
		t.Fatalf("kept %d samples, want the read to stop early", len(got))
	}
	_ = rep
}

type cancelAfterSource struct {
	data   []byte
	cancel context.CancelFunc
	after  int
}

func (c cancelAfterSource) Manifest(context.Context) (ManifestRead, error) {
	return ManifestRead{}, nil
}
func (c cancelAfterSource) Status(context.Context) (*StatusDoc, error) { return nil, nil }
func (c cancelAfterSource) OpenFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(&cancelReader{r: bytes.NewReader(c.data), cancel: c.cancel, left: c.after}), nil
}

type cancelReader struct {
	r      *bytes.Reader
	cancel context.CancelFunc
	left   int
}

func (c *cancelReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		c.cancel()
	}
	if len(p) > c.left && c.left > 0 {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= n
	return n, err
}

/* ------------------------------------- W12-04: bounded read admission */

// hangCountAccess blocks like a dead share and counts outstanding calls.
type hangCountAccess struct {
	mu      sync.Mutex
	active  int
	max     int
	release chan struct{}
}

func (a *hangCountAccess) enter() {
	a.mu.Lock()
	a.active++
	if a.active > a.max {
		a.max = a.active
	}
	a.mu.Unlock()
}
func (a *hangCountAccess) leave() {
	a.mu.Lock()
	a.active--
	a.mu.Unlock()
}
func (a *hangCountAccess) peak() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.max
}
func (a *hangCountAccess) ReadFile(string) ([]byte, error) {
	a.enter()
	defer a.leave()
	<-a.release
	return nil, errors.New("late")
}
func (a *hangCountAccess) Open(string) (io.ReadCloser, error) {
	a.enter()
	defer a.leave()
	<-a.release
	return nil, errors.New("late")
}

// Repeated permitted reads of a dead share return on their deadline and leave
// at most the bounded number of outstanding OS helpers — per source and
// globally — not one per read.
func TestReadAdmissionBoundsOutstandingOSCalls(t *testing.T) {
	loc, _ := ParseLocation(`\gone\share`)
	acc := &hangCountAccess{release: make(chan struct{})}
	src := &FolderSource{Loc: loc, Access: acc}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			if _, err := src.Manifest(ctx); err == nil {
				t.Error("a dead share answered")
			}
		}()
	}
	wg.Wait()
	if peak := acc.peak(); peak > maxSourceOutstanding {
		t.Fatalf("one source left %d outstanding OS calls, want at most %d", peak, maxSourceOutstanding)
	}
	close(acc.release)
	waitDrained(t, acc)

	// Across sources the GLOBAL pool bounds the outstanding calls.
	accs := make([]*hangCountAccess, 6)
	var wg2 sync.WaitGroup
	for i := range accs {
		accs[i] = &hangCountAccess{release: make(chan struct{})}
		s := &FolderSource{Loc: loc, Access: accs[i]}
		for j := 0; j < 8; j++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
				defer cancel()
				_, _ = s.Manifest(ctx)
			}()
		}
	}
	wg2.Wait()
	total := 0
	for _, a := range accs {
		total += a.peak()
	}
	if total > maxOutstandingOS {
		t.Fatalf("global outstanding OS calls peaked at %d, want at most %d", total, maxOutstandingOS)
	}
	for _, a := range accs {
		close(a.release)
	}
	for _, a := range accs {
		waitDrained(t, a)
	}
}

func waitDrained(t *testing.T, a *hangCountAccess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.Lock()
		n := a.active
		a.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d OS helpers never finished", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

/* ------------------------------------- W12-05: containment, no links */

// A valid recording basename that is itself a symlink is refused; the plain
// file beside it still opens. Only fake temp-dir data is used.
func TestFolderOpenRefusesFileSymlink(t *testing.T) {
	dir := t.TempDir()
	name := FileNameFor(t0)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sampleLine(t0, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(private, []byte("PRIVATE-FAKE-DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkName := FileNameFor(t0.Add(time.Hour))
	if err := os.Symlink(private, filepath.Join(dir, linkName)); err != nil {
		t.Skipf("symlink not creatable here: %v", err)
	}
	src := folderSource(t, dir)
	_, err := src.OpenFile(context.Background(), linkName)
	if KindOf(err) != ReadBadData {
		t.Fatalf("link entry: kind %v err %v, want bad-data", KindOf(err), err)
	}
	if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), private) {
		t.Fatalf("refusal leaks the target: %v", err)
	}
	rc, err := src.OpenFile(context.Background(), name)
	if err != nil {
		t.Fatalf("plain file refused: %v", err)
	}
	rc.Close()
}

// A junction (Windows) or symlink ancestor substitutes the whole folder: the
// open is refused before any target byte is read.
func TestFolderRefusesLinkAncestor(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	name := FileNameFor(t0)
	if err := os.WriteFile(filepath.Join(real, name), []byte(sampleLine(t0, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, ManifestName), []byte(`{"format":1,"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, real).CombinedOutput(); err != nil {
			t.Skipf("junction not creatable here: %v %s", err, out)
		}
	} else {
		if err := os.Symlink(real, alias); err != nil {
			t.Skipf("symlink not creatable here: %v", err)
		}
	}
	src := folderSource(t, alias)
	if _, err := src.Manifest(context.Background()); KindOf(err) != ReadBadData {
		t.Fatalf("junction ancestor manifest: kind %v err %v", KindOf(err), err)
	}
	if _, err := src.OpenFile(context.Background(), name); KindOf(err) != ReadBadData {
		t.Fatalf("junction ancestor file: kind %v err %v", KindOf(err), err)
	}
	// The real folder still reads.
	ok := folderSource(t, real)
	if _, err := ok.Manifest(context.Background()); err != nil {
		t.Fatalf("real folder refused: %v", err)
	}
}

// A junction AS the recording entry (a directory alias with a valid basename)
// is refused: it is not a regular file.
func TestFolderRefusesJunctionEntry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	name := FileNameFor(t0)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, name), target).CombinedOutput(); err != nil {
		t.Skipf("junction not creatable here: %v %s", err, out)
	}
	src := folderSource(t, dir)
	if _, err := src.OpenFile(context.Background(), name); KindOf(err) != ReadBadData {
		t.Fatalf("junction entry: kind %v err %v", KindOf(err), err)
	}
}

// A non-regular object (a directory with a valid recording basename) is refused.
func TestFolderRefusesNonRegularEntry(t *testing.T) {
	dir := t.TempDir()
	name := FileNameFor(t0)
	if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	src := folderSource(t, dir)
	if _, err := src.OpenFile(context.Background(), name); KindOf(err) != ReadBadData {
		t.Fatalf("directory entry: kind %v err %v", KindOf(err), err)
	}
}

// The manifest open has the same policy: a manifest.json that is a link is
// refused, not followed.
func TestFolderManifestRefusesLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(real, []byte(`{"format":1,"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, ManifestName)); err != nil {
		t.Skipf("symlink not creatable here: %v", err)
	}
	src := folderSource(t, dir)
	if _, err := src.Manifest(context.Background()); KindOf(err) != ReadBadData {
		t.Fatalf("manifest link: kind %v err %v", KindOf(err), err)
	}
}

// The explicit trusted-root policy allows links; nothing sets it silently.
func TestFolderTrustedRootAllowsLinks(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(t.TempDir(), "target.ndjson")
	if err := os.WriteFile(private, []byte(sampleLine(t0, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	name := FileNameFor(t0)
	if err := os.Symlink(private, filepath.Join(dir, name)); err != nil {
		t.Skipf("symlink not creatable here: %v", err)
	}
	loc, err := ParseLocation(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := &FolderSource{Loc: loc, Access: DirAccess{Dir: dir, Trusted: true}}
	rc, err := src.OpenFile(context.Background(), name)
	if err != nil {
		t.Fatalf("trusted root refused a link: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !strings.Contains(string(b), `"cpu":5`) {
		t.Fatalf("trusted root did not read the target: %q", b)
	}
}
