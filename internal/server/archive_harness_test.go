package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

// The harness of the archive scenarios (P3 proof, A1-A18 and INJ-1 to INJ-10).
// archPC is a cfsPC whose handler has the recovery hooks wired, and which can be
// "killed": restart builds a new store, session and handler on the same folders,
// the way a new process meets what a dead one left behind.

type archPC struct {
	*cfsPC
	rec  *casestore.Recoverer
	arec *ArchiveRecovery
}

func newArchPC(t *testing.T, pass string) *archPC {
	t.Helper()
	pc := newCFSPC(t, pass, pass != "")
	a := &archPC{cfsPC: pc}
	// Cases made before a master key exists are sealed with the test protector.
	oldDPAPI := caseFileDPAPI
	caseFileDPAPI = func() (sealedfile.Protector, error) { return pc.prot, nil }
	t.Cleanup(func() { caseFileDPAPI = oldDPAPI })
	a.wire()
	steppingClock(t)
	return a
}

// wire builds the handler over the current store with the hooks registered, as
// cmd/audit-explorer does.
func (a *archPC) wire() {
	a.t.Helper()
	rec, err := casestore.NewRecoverer(a.store)
	if err != nil {
		a.t.Fatal(err)
	}
	ar := NewArchiveRecovery(a.store, a.root)
	if err := ar.Register(rec); err != nil {
		a.t.Fatal(err)
	}
	a.rec = rec
	a.h = New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(a.store, mustHosts(a.t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(a.sess), WithRecoverer(rec), WithArchiveRecovery(ar),
	)
	a.arec = ar
}

// restart is the kill: the old store is closed without any job finishing, then a
// new process starts on the same folders, runs the start-up recovery and the
// master key is unlocked again.
func (a *archPC) restart() casestore.RecoverReport {
	a.t.Helper()
	_ = a.store.Close()
	a.sess = NewMasterKeySession(a.dir)
	store, err := sqlite.New(sqlite.Options{
		Root: a.root, Protector: a.prot, MinFreeBytes: -1,
		Protectors: a.sess.Lookup, NewCaseKind: a.sess.NewCaseKind,
		LegacyCheck: a.sess.LegacyCheck, LegacyDone: a.sess.LegacyDone,
		KeyParams: cfsFast,
	})
	if err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() { _ = store.Close() })
	a.store = store
	a.wire()
	if a.pass != "" {
		if res := a.do(http.MethodPost, "/api/master-key/unlock", unlockBody(a.pass)); res.Code != http.StatusOK {
			a.t.Fatalf("unlock after restart = %d %s", res.Code, res.Body)
		}
	}
	if err := a.arec.CleanVerify(); err != nil {
		a.t.Fatal(err)
	}
	rep, err := a.rec.RecoverAll(context.Background())
	if err != nil {
		a.t.Fatal(err)
	}
	return rep
}

// steppingClock gives every archiveNow call a later second, so two archives of a
// case never get the same file name and a retry always has a new one.
func steppingClock(t *testing.T) {
	t.Helper()
	old := archiveNow
	base := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	var n atomic.Int64
	archiveNow = func() time.Time { return base.Add(time.Duration(n.Add(1)) * time.Second) }
	t.Cleanup(func() { archiveNow = old })
}

// req sends a request that carries the page's belief (X-Expect-State).
func (a *archPC) req(method, path, body, belief string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if belief != "" {
		req.Header.Set("X-Expect-State", belief)
	}
	res := httptest.NewRecorder()
	a.h.ServeHTTP(res, req)
	return res
}

func (a *archPC) handler() *handler { return a.h.(*handler) }

// folder is a fresh folder the archive routes accept, outside the data folder.
func (a *archPC) folder() string {
	a.t.Helper()
	d := a.t.TempDir()
	if _, err := realArchiveEnv().resolveArchiveFolder(d, a.root); err != nil {
		a.t.Skip("temp directory is not a folder the archive routes accept")
	}
	return d
}

func (a *archPC) archiveReq(id, folder, belief string) *httptest.ResponseRecorder {
	return a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(folder)+`}`, belief)
}

func (a *archPC) jobOf(id, kind string) fileJobDTO {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/cases/"+id+"/"+kind, "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("GET %s = %d %s", kind, res.Code, res.Body)
	}
	var j fileJobDTO
	decodeBody(a.t, res, &j)
	return j
}

// wait polls the job until it is no longer running.
func (a *archPC) wait(id, kind string) fileJobDTO {
	a.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if j := a.jobOf(id, kind); j.State != fileJobRunning {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.t.Fatalf("%s job did not end", kind)
	return fileJobDTO{}
}

// archiveDone archives a case and waits for the job; it must succeed.
func (a *archPC) archiveDone(id, folder, belief string) fileJobDTO {
	a.t.Helper()
	res := a.archiveReq(id, folder, belief)
	if res.Code != http.StatusAccepted {
		a.t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	j := a.wait(id, "archive")
	if j.State != fileJobDone {
		a.t.Fatalf("archive job = %+v", j)
	}
	return j
}

func (a *archPC) verifyDone(id string) fileJobDTO {
	a.t.Helper()
	res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived")
	if res.Code != http.StatusAccepted {
		a.t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	j := a.wait(id, "verify")
	if j.State != fileJobDone {
		a.t.Fatalf("verify job = %+v", j)
	}
	return j
}

func (a *archPC) freeReq(id string) *httptest.ResponseRecorder {
	return a.req(http.MethodPost, "/api/cases/"+id+"/free", `{"confirm":`+q(shortCode(casestore.CaseID(id)))+`}`, "archived")
}

func (a *archPC) entry(id string) casestore.CaseInfo {
	a.t.Helper()
	info, err := a.store.Entry(context.Background(), casestore.CaseID(id))
	if err != nil {
		a.t.Fatal(err)
	}
	return info
}

func (a *archPC) state(id string) casestore.State { return casestore.StateOf(a.entry(id)) }

// files lists the names in a folder that end with the suffix.
func listNames(t *testing.T, dir, suffix string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func caseFilesIn(t *testing.T, dir string) []string { return listNames(t, dir, casestore.CaseFileExt) }
func partsIn(t *testing.T, dir string) []string     { return listNames(t, dir, ".part") }

type orphanReply struct {
	Files                []orphanFileDTO `json:"files"`
	VerifyCleanupPending bool            `json:"verifyCleanupPending"`
}

func (a *archPC) orphans() orphanReply {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/cases/orphan-files", "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("orphan-files = %d %s", res.Code, res.Body)
	}
	var o orphanReply
	decodeBody(a.t, res, &o)
	return o
}

func (a *archPC) listAll() casesPage {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/cases", "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var p casesPage
	decodeBody(a.t, res, &p)
	return p
}

type casesPage struct {
	Cases         []caseDTO `json:"cases"`
	ArchivedTotal int       `json:"archivedTotal"`
	Offset        int       `json:"offset"`
	PageSize      int       `json:"pageSize"`
}

func (p casesPage) find(id string) (caseDTO, bool) {
	for _, c := range p.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return caseDTO{}, false
}

// A pause holds the archive job at a point until the test lets it go.
type pause struct {
	reached chan struct{}
	proceed chan struct{}
	once    sync.Once
}

// pauseAt installs the hook; it is removed when the test ends.
func pauseAt(t *testing.T, point string) *pause {
	t.Helper()
	p := &pause{reached: make(chan struct{}), proceed: make(chan struct{})}
	archiveInject = func(pt, _, _ string) bool {
		if pt != point {
			return false
		}
		first := false
		p.once.Do(func() { first = true; close(p.reached) })
		if first {
			select {
			case <-p.proceed:
			case <-time.After(20 * time.Second):
			}
		}
		return false
	}
	t.Cleanup(func() { archiveInject = nil })
	return p
}

func (p *pause) waitReached(t *testing.T) {
	t.Helper()
	select {
	case <-p.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the job never reached the pause")
	}
}

func (p *pause) release() {
	select {
	case <-p.proceed:
	default:
		close(p.proceed)
	}
}

// killAt installs a hook that kills the job at the point.
func killAt(t *testing.T, point string) {
	t.Helper()
	archiveInject = func(pt, _, _ string) bool { return pt == point }
	t.Cleanup(func() { archiveInject = nil })
}

// waitKilled waits until the killed job has returned (its busy lock is released).
func (a *archPC) waitKilled(id string) {
	a.t.Helper()
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))
	if job == nil {
		a.t.Fatal("no job")
	}
	select {
	case <-job.done:
	case <-time.After(20 * time.Second):
		a.t.Fatal("the killed job did not return")
	}
}

func archReason(t *testing.T, res *httptest.ResponseRecorder, code int, reason, text string) {
	t.Helper()
	if res.Code != code {
		t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, code, reason)
	}
	var f caseFailure
	if err := json.Unmarshal(res.Body.Bytes(), &f); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	if f.Reason != reason || (text != "" && f.Error != text) {
		t.Fatalf("failure = %+v, want %q %q", f, reason, text)
	}
}

func nameOf(t *testing.T, p string) string { t.Helper(); return filepath.Base(p) }
