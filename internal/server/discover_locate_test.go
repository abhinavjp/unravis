package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
)

// Everything here is synthetic: the folders, the machine and the log lines.

const (
	locAppDir   = `D:\Apps\Orders\Logs`
	locOtherDir = `D:\Apps\Misc`
	locFinding  = "loc00001"
)

var locLog4net = "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.OrdersController - Request handled\n" +
	"2001-10-25 14:02:31,123 [17] WARN  Synthetic.Api.OrdersController - Slow call\n" +
	"2001-10-25 14:02:32,004 [12] ERROR Synthetic.Api.OrdersController - Failed\n"

func locBody(path string) string {
	b, _ := json.Marshal(map[string]string{"path": path})
	return string(b)
}

func locURL(scan, finding string) string {
	return "/api/discover/scans/" + scan + "/findings/" + finding + "/locate"
}

// locFix is a finished scan with one planted finding: a log set whose folder the
// scan could not find.
type locFix struct {
	*discFix
	sid string
}

func newLocFix(t *testing.T, tweak func(*DiscoverConfig)) *locFix {
	t.Helper()
	f := newDiscFix(t, tweak)
	scan, _ := f.scan()
	rec, st := f.e.handler().disc.lookup(scan)
	if st != scanFound {
		t.Fatalf("scan lookup = %v", st)
	}
	fd := discover.Finding{
		ID: locFinding, Detector: "applog", Kind: discover.KindLogSet,
		Display: "Application log", Unreadable: "discover.cannotopen.shows",
	}
	rec.mu.Lock()
	rec.scan.Findings = append(rec.scan.Findings, fd)
	rec.byID[locFinding] = &scanFinding{f: fd}
	rec.mu.Unlock()

	f.fe.FS.AddFile(locAppDir+`\orders.log`, int64(len(locLog4net)), discDay, []byte(locLog4net))
	f.fe.FS.AddFile(locAppDir+`\orders.log.1`, int64(len(locLog4net)), discDay.AddDate(0, 0, -1), []byte(locLog4net))
	f.fe.FS.AddFile(locAppDir+`\readme.md`, 5, discDay, []byte("hello"))
	f.fe.FS.AddFile(locOtherDir+`\notes.log`, 11, discDay, []byte("hello world"))
	f.fe.FS.AddFile(locOtherDir+`\empty.log`, 0, discDay, nil)
	f.fe.FS.AddFile(`D:\Apps\Bin\app.dll`, 3, discDay, []byte("MZ."))
	return &locFix{discFix: f, sid: scan}
}

func (f *locFix) locate(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.req(http.MethodPost, locURL(f.sid, locFinding), locBody(path))
}

// finding returns the planted finding as the scan reports it.
func (f *locFix) finding() discover.Finding {
	f.t.Helper()
	sc := f.getScan(f.sid)
	return findingWhere(f.t, sc, "planted", func(fd discover.Finding) bool { return fd.ID == locFinding })
}

func (f *locFix) stored() *scanFinding {
	rec, _ := f.e.handler().disc.lookup(f.sid)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	cp := *rec.byID[locFinding]
	return &cp
}

func (f *locFix) wantUnchanged(before discover.Finding) {
	f.t.Helper()
	after := f.finding()
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		f.t.Errorf("the finding changed:\nbefore %s\nafter  %s", a, b)
	}
	if len(f.stored().f.Paths()) != 0 {
		f.t.Error("the stored finding gained paths")
	}
}

func TestLocateGoodFolderVerifiesTheFinding(t *testing.T) {
	f := newLocFix(t, nil)
	res := f.locate(locAppDir)
	if res.Code != http.StatusOK {
		t.Fatalf("locate = %d %s", res.Code, res.Body)
	}
	var out locateResponse
	decodeBody(t, res, &out)
	if !out.Verified || out.Reason != "" || out.Files != 2 || out.Finding == nil {
		t.Fatalf("answer = %+v", out)
	}
	// Only the log files count: readme.md is left out. The answer lists no names.
	var raw map[string]json.RawMessage
	decodeBody(t, res, &raw)
	for k := range raw {
		if k != "verified" && k != "files" && k != "reason" && k != "finding" {
			t.Errorf("unexpected key %q in the answer", k)
		}
	}
	if strings.Contains(strings.ToLower(res.Body.String()), "readme") || strings.Contains(res.Body.String(), "orders.log.1") {
		t.Errorf("the answer lists file names: %s", res.Body)
	}

	fd := f.finding()
	if !fd.Verified || fd.Importer != "log4net-text" || fd.Unreadable != "" || fd.Files != 2 {
		t.Errorf("finding after = %+v", fd)
	}
	if fd.Newest.Before(fd.Oldest) || fd.SizeBytes != int64(2*len(locLog4net)) {
		t.Errorf("counts after = size %d oldest %v newest %v", fd.SizeBytes, fd.Oldest, fd.Newest)
	}
	sf := f.stored()
	if got := sf.f.Paths(); len(got) != 2 || len(sf.files) != 2 {
		t.Errorf("stored paths %v, files %d", got, len(sf.files))
	}
	if !sf.files[0].mod.Before(sf.files[1].mod) {
		t.Error("the files are not ordered oldest first")
	}
}

func TestLocateASingleFile(t *testing.T) {
	f := newLocFix(t, nil)
	res := f.locate(locAppDir + `\orders.log`)
	var out locateResponse
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || !out.Verified || out.Files != 1 {
		t.Fatalf("locate = %d %s", res.Code, res.Body)
	}
	if fd := f.finding(); fd.Kind != discover.KindLogFile || fd.Importer != "log4net-text" {
		t.Errorf("finding = %+v", fd)
	}
}

func TestLocateIsIdempotent(t *testing.T) {
	f := newLocFix(t, nil)
	first := f.locate(locAppDir)
	afterFirst := f.finding()
	second := f.locate(locAppDir)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("locate = %d, %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("the repeat answered differently:\n%s\n%s", first.Body, second.Body)
	}
	a, _ := json.Marshal(afterFirst)
	b, _ := json.Marshal(f.finding())
	if string(a) != string(b) {
		t.Errorf("the repeat changed the finding:\n%s\n%s", a, b)
	}
	if got := len(f.stored().f.Paths()); got != 2 {
		t.Errorf("paths after the repeat = %d, want 2", got)
	}
}

func TestLocateFailuresGiveAReasonAndChangeNothing(t *testing.T) {
	cases := []struct {
		name, path, reason string
	}{
		{"unrelated text", locOtherDir + `\notes.log`, locateWrongFormat},
		{"unrelated folder", locOtherDir, locateWrongFormat},
		{"no log files", `D:\Apps\Bin`, locateNoLogs},
		{"missing", `D:\Apps\Nowhere`, locateNotFound},
	}
	f := newLocFix(t, nil)
	before := f.finding()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.locate(c.path)
			if res.Code != http.StatusOK {
				t.Fatalf("locate = %d %s", res.Code, res.Body)
			}
			var out locateResponse
			decodeBody(t, res, &out)
			if out.Verified || out.Reason != c.reason || out.Finding != nil {
				t.Errorf("answer = %+v, want reason %q", out, c.reason)
			}
			f.wantUnchanged(before)
		})
	}
}

func TestLocateEmptyAndDeniedFiles(t *testing.T) {
	f := newLocFix(t, nil)
	before := f.finding()
	f.fe.FS.AddDir(`D:\Apps\Quiet`)
	f.fe.FS.AddFile(`D:\Apps\Quiet\a.log`, 0, discDay, nil)
	f.fe.FS.AddFile(`D:\Apps\Locked\a.log`, 4, discDay, []byte("data"))
	f.fe.FS.Deny(`D:\Apps\Locked`)
	for path, want := range map[string]string{
		`D:\Apps\Quiet`:  locateEmptyFile,
		`D:\Apps\Locked`: locateCannotOpen,
	} {
		var out locateResponse
		res := f.locate(path)
		decodeBody(t, res, &out)
		if res.Code != http.StatusOK || out.Verified || out.Reason != want {
			t.Errorf("%s: %d %s, want reason %q", path, res.Code, res.Body, want)
		}
	}
	f.wantUnchanged(before)
}

func TestLocateRefusesUnsafePaths(t *testing.T) {
	f := newLocFix(t, nil)
	f.fe.FS.AddReparse(locAppDir + `\link`)
	f.fe.FS.AddFile(locAppDir+`\link\orders.log`, int64(len(locLog4net)), discDay, []byte(locLog4net))
	before := f.finding()
	cases := []struct {
		name, path, reason string
	}{
		{"relative", `Logs\orders.log`, locateNotAbsolute},
		{"no drive", `\Apps\Orders`, locateNotAbsolute},
		{"empty", ``, locateNotAbsolute},
		{"unc", `\\files01\share\logs`, locateNotLocal},
		{"device prefix", `\\?\D:\Apps\Orders\Logs`, locateNotLocal},
		{"dotdot", locAppDir + `\..\Misc\notes.log`, locateUnsafe},
		{"junction folder", locAppDir + `\link`, locateUnsafe},
		{"through a junction", locAppDir + `\link\orders.log`, locateUnsafe},
		{"4 KiB path", `D:\` + strings.Repeat("a", 4096), locateUnsafe},
		{"secret word", `D:\Apps\password\orders.log`, locateUnsafe},
		{"alternate stream", locAppDir + `\orders.log:hidden`, locateUnsafe},
		{"control char", locAppDir + "\x00\\orders.log", locateUnsafe},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.locate(c.path)
			wantReason(t, res, http.StatusBadRequest, c.reason)
			f.wantUnchanged(before)
		})
	}
}

func TestLocateRefusalsDoNotEchoTheInput(t *testing.T) {
	f := newLocFix(t, nil)
	for _, p := range []string{`\\files01\hidden-share\x`, `D:\Apps\..\secret-marker`, `relative-marker`} {
		res := f.locate(p)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", p, res.Code)
		}
		low := strings.ToLower(res.Body.String())
		for _, bad := range []string{"hidden-share", "files01", "secret-marker", "relative-marker"} {
			if strings.Contains(low, bad) {
				t.Errorf("the refusal echoes %q: %s", bad, res.Body)
			}
		}
	}
}

func TestLocateNeedsTheTokenAndALocalOrigin(t *testing.T) {
	f := newLocFix(t, nil)
	before := f.finding()
	do := func(token, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, locURL(f.sid, locFinding), strings.NewReader(locBody(locAppDir)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Audit-Token", token)
		}
		if host != "" {
			req.Host = host
			res := httptest.NewRecorder()
			f.e.h.ServeHTTP(res, req)
			return res
		}
		return f.e.serve(req)
	}
	if res := do("", ""); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d %s", res.Code, res.Body)
	}
	if res := do("wrong", ""); res.Code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d", res.Code)
	}
	if res := do(testToken, "evil.example:8765"); res.Code != http.StatusForbidden {
		t.Errorf("foreign host = %d %s", res.Code, res.Body)
	}
	f.wantUnchanged(before)
}

func TestLocateMethodAndBodyChecks(t *testing.T) {
	f := newLocFix(t, nil)
	url := locURL(f.sid, locFinding)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if res := f.req(m, url, ""); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d", m, res.Code)
		}
	}
	for name, body := range map[string]string{
		"not json":      `nope`,
		"unknown field": `{"path":"D:\\Apps","extra":1}`,
	} {
		if res := f.req(http.MethodPost, url, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
}

func TestLocateUnknownScanFindingAndKind(t *testing.T) {
	f := newLocFix(t, nil)
	if res := f.req(http.MethodPost, locURL(strings.Repeat("b", 32), locFinding), locBody(locAppDir)); res.Code != http.StatusNotFound {
		t.Errorf("unknown scan = %d", res.Code)
	} else {
		wantReason(t, res, http.StatusNotFound, "scan-unknown")
	}
	wantReason(t, f.req(http.MethodPost, locURL(f.sid, "nosuchone"), locBody(locAppDir)), http.StatusNotFound, "finding-unknown")
	wantReason(t, f.req(http.MethodPost, locURL(f.sid, "bad!id"), locBody(locAppDir)), http.StatusNotFound, "finding-unknown")

	// A finding that is not a log file cannot be completed with a folder.
	_, sc := f.scan()
	iis := f.iisFinding(sc)
	rec, _ := f.e.handler().disc.lookup(f.sid)
	rec.mu.Lock()
	rec.byID[iis.ID] = &scanFinding{f: iis}
	rec.mu.Unlock()
	if iis.Kind == discover.KindLogFile || iis.Kind == discover.KindLogSet {
		t.Skip("the fixture's IIS finding is a log set")
	}
	wantReason(t, f.req(http.MethodPost, locURL(f.sid, iis.ID), locBody(locAppDir)), http.StatusConflict, "not-locatable")
}

func TestLocateOnAnExpiredScanIs404(t *testing.T) {
	f := newLocFix(t, nil)
	f.clk.advance(2 * time.Hour)
	wantReason(t, f.locate(locAppDir), http.StatusNotFound, "scan-expired")
}

func TestLocateIsRateLimited(t *testing.T) {
	f := newLocFix(t, nil)
	var last *httptest.ResponseRecorder
	for i := 0; i <= locatesPerWindow; i++ {
		last = f.locate(`D:\Apps\Nowhere`)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("call %d = %d, want 429", locatesPerWindow+1, last.Code)
	}
	// A refused path does not use up the allowance.
	g := newLocFix(t, nil)
	for i := 0; i < locatesPerWindow+5; i++ {
		if res := g.locate(`\\files01\share`); res.Code != http.StatusBadRequest {
			t.Fatalf("refusal %d = %d", i, res.Code)
		}
	}
	if res := g.locate(locAppDir); res.Code != http.StatusOK {
		t.Errorf("after refusals = %d %s", res.Code, res.Body)
	}
}

// blockingFS holds Final until released, to show the work is bounded.
type blockingFS struct {
	discover.FS
	mu      sync.Mutex
	blocked bool
	release chan struct{}
}

func (b *blockingFS) Final(p string) (string, bool, error) {
	b.mu.Lock()
	blocked := b.blocked
	b.mu.Unlock()
	if blocked {
		<-b.release
	}
	return b.FS.Final(p)
}

func TestLocateGivesUpOnAStuckFileSystem(t *testing.T) {
	old := locateBudget
	locateBudget = 100 * time.Millisecond
	t.Cleanup(func() { locateBudget = old })

	bfs := &blockingFS{release: make(chan struct{})}
	t.Cleanup(func() { close(bfs.release) })
	f := newLocFix(t, func(c *DiscoverConfig) {
		bfs.FS = c.Env.FS
		c.Env.FS = bfs
	})
	before := f.finding()
	bfs.mu.Lock()
	bfs.blocked = true
	bfs.mu.Unlock()

	start := time.Now()
	res := f.locate(locAppDir)
	var out locateResponse
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Verified || out.Reason != locateTimedOut {
		t.Errorf("locate = %d %s", res.Code, res.Body)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the call was not bounded")
	}
	f.wantUnchanged(before)
}

// gateFS holds Final until the gate opens and counts the calls that follow it, to
// show what a worker does after its deadline.
type gateFS struct {
	discover.FS
	mu       sync.Mutex
	gated    bool
	gate     chan struct{}
	entered  chan struct{}
	lstats   int
	readDirs int
}

func newGateFS() *gateFS {
	return &gateFS{gate: make(chan struct{}), entered: make(chan struct{}, 16)}
}

func (g *gateFS) setGated(v bool) {
	g.mu.Lock()
	g.gated = v
	g.mu.Unlock()
}

func (g *gateFS) open() {
	defer func() { _ = recover() }() // already open
	close(g.gate)
}

func (g *gateFS) Final(p string) (string, bool, error) {
	g.mu.Lock()
	gated := g.gated
	g.mu.Unlock()
	if gated {
		g.entered <- struct{}{}
		<-g.gate
	}
	return g.FS.Final(p)
}

func (g *gateFS) Lstat(p string) (discover.FileInfo, error) {
	g.mu.Lock()
	g.lstats++
	g.mu.Unlock()
	return g.FS.Lstat(p)
}

func (g *gateFS) ReadDir(p string) ([]discover.FileInfo, error) {
	g.mu.Lock()
	g.readDirs++
	g.mu.Unlock()
	return g.FS.ReadDir(p)
}

func (g *gateFS) calls() (lstats, readDirs int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lstats, g.readDirs
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLocateWorkerCeilingHoldsAndFreesWhenWorkersFinish(t *testing.T) {
	oldBudget, oldSlots := locateBudget, locateSlots
	locateBudget = 50 * time.Millisecond
	locateSlots = make(chan struct{}, 2)
	t.Cleanup(func() { locateBudget, locateSlots = oldBudget, oldSlots })

	gfs := newGateFS()
	t.Cleanup(gfs.open)
	f := newLocFix(t, func(c *DiscoverConfig) {
		gfs.FS = c.Env.FS
		c.Env.FS = gfs
	})
	gfs.setGated(true)

	// Two stuck workers fill the slots. Each request still gives up on time.
	for i := 0; i < 2; i++ {
		res := f.locate(locAppDir)
		var out locateResponse
		decodeBody(t, res, &out)
		if res.Code != http.StatusOK || out.Reason != locateTimedOut {
			t.Fatalf("call %d = %d %s", i+1, res.Code, res.Body)
		}
	}
	if len(locateSlots) != 2 {
		t.Fatalf("slots in use after the requests returned = %d, want 2 (workers still stuck)", len(locateSlots))
	}

	// The next call does not start a worker: 429 with the fixed error shape.
	res := f.locate(locAppDir)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("third call = %d %s, want 429", res.Code, res.Body)
	}
	var body map[string]string
	decodeBody(t, res, &body)
	if body["error"] == "" || len(body) != 1 {
		t.Errorf("429 body = %s, want only a fixed error text", res.Body)
	}
	if strings.Contains(res.Body.String(), `D:\`) || strings.Contains(res.Body.String(), "Apps") {
		t.Errorf("429 body echoes the path: %s", res.Body)
	}
	if len(locateSlots) != 2 {
		t.Errorf("a refused call changed the slots: %d", len(locateSlots))
	}

	// Slots free when the workers finish, not before.
	gfs.setGated(false)
	gfs.open()
	waitUntil(t, "workers to release their slots", func() bool { return len(locateSlots) == 0 })
	res = f.locate(locAppDir)
	if res.Code != http.StatusOK {
		t.Errorf("after the workers finished = %d %s", res.Code, res.Body)
	}
	if len(locateSlots) != 0 {
		t.Errorf("slot leaked after a normal call: %d", len(locateSlots))
	}
}

func TestLocateDeadlineStopsTheWalkerBetweenSteps(t *testing.T) {
	old := locateBudget
	locateBudget = 50 * time.Millisecond
	t.Cleanup(func() { locateBudget = old })

	gfs := newGateFS()
	t.Cleanup(gfs.open)
	f := newLocFix(t, func(c *DiscoverConfig) {
		gfs.FS = c.Env.FS
		c.Env.FS = gfs
	})
	before := f.finding()
	gfs.setGated(true)
	gfs.mu.Lock()
	gfs.lstats, gfs.readDirs = 0, 0 // the scan in the fixture used them already
	gfs.mu.Unlock()

	res := f.locate(locAppDir)
	var out locateResponse
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Reason != locateTimedOut {
		t.Fatalf("locate = %d %s", res.Code, res.Body)
	}
	<-gfs.entered

	// The stuck call now returns, after the deadline. The worker must stop there.
	gfs.setGated(false)
	gfs.open()
	waitUntil(t, "the worker to end", func() bool { return len(locateSlots) == 0 })
	if lstats, readDirs := gfs.calls(); lstats != 0 || readDirs != 0 {
		t.Errorf("the worker kept walking after its deadline: Lstat %d, ReadDir %d", lstats, readDirs)
	}
	f.wantUnchanged(before)
}

func TestRunWithinReleasesTheSlotWhenTheWorkerEndsEvenOnPanic(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, err := runWithin(context.Background(), slots, 30*time.Millisecond, func(context.Context) int { panic("boom") })
	if err == nil {
		t.Error("a panicking worker gave an answer")
	}
	waitUntil(t, "slot release after panic", func() bool { return len(slots) == 0 })
}
