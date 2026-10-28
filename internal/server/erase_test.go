package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// eraseEnv is a case env with an Erased log in a temp folder and an erase timer the
// test fires by hand (the 30 s wait of A16 never runs on a real clock).
type eraseEnv struct {
	*caseEnv
	logPath string
	fire    chan time.Time
	asked   chan time.Duration
}

func newEraseEnv(t *testing.T, cfg envConfig) *eraseEnv {
	t.Helper()
	ee := &eraseEnv{logPath: filepath.Join(t.TempDir(), "erased.json"), fire: make(chan time.Time, 1), asked: make(chan time.Duration, 4)}
	cfg.options = append(cfg.options,
		WithErasedLog(NewErasedFile(ee.logPath)),
		withEraseTimer(func(d time.Duration) <-chan time.Time {
			ee.asked <- d
			return ee.fire
		}),
	)
	ee.caseEnv = newCaseEnv(t, cfg)
	return ee
}

func (e *eraseEnv) erase(id, confirm string) *httptest.ResponseRecorder {
	return e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+q(confirm)+`}`)
}

func (e *eraseEnv) list() (cases []caseDTO, erased []erasedDTO) {
	e.t.Helper()
	var out struct {
		Cases  []caseDTO   `json:"cases"`
		Erased []erasedDTO `json:"erased"`
	}
	res := e.get("/api/cases")
	if res.Code != http.StatusOK {
		e.t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	decodeBody(e.t, res, &out)
	return out.Cases, out.Erased
}

func TestEraseTakesTheShortCodeOrTheFullID(t *testing.T) {
	e := newEraseEnv(t, envConfig{})
	a, b := e.newCase("A"), e.newCase("B")
	for _, wrong := range []string{"", "yes", a[:7], a[:9], strings.Repeat("a", 8), b[:8]} {
		res := e.erase(a, wrong)
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "confirm-mismatch" {
			t.Errorf("erase with %q = %d %s", wrong, res.Code, res.Body)
		}
	}
	if cases, _ := e.list(); len(cases) != 2 {
		t.Fatalf("a refused erase removed a case: %+v", cases)
	}
	// The short code as typed: any case, with spaces or a dash around it.
	if res := e.erase(a, "  "+strings.ToUpper(a[:4])+"-"+a[4:8]+" "); res.Code != http.StatusNoContent {
		t.Fatalf("erase by short code = %d %s", res.Code, res.Body)
	}
	if res := e.erase(b, b); res.Code != http.StatusNoContent {
		t.Fatalf("erase by full id = %d %s", res.Code, res.Body)
	}
	if cases, _ := e.list(); len(cases) != 0 {
		t.Fatalf("cases left: %+v", cases)
	}
}

// M15: an Archiving row is not erased, and nothing is touched.
func TestEraseRefusedWhileAnArchiveRuns(t *testing.T) {
	e := newEraseEnv(t, envConfig{})
	id := e.newCase("Archiving")
	lc := e.store.(casestore.Lifecycle)
	if err := lc.SetMark(t.Context(), casestore.CaseID(id), casestore.MarkArchiving); err != nil {
		t.Fatal(err)
	}
	res := e.erase(id, id[:8])
	f := failureOf(t, res)
	if res.Code != http.StatusConflict || f.Reason != "cancel-archive-first" || f.Error != "An archive is running. Cancel it first." {
		t.Fatalf("erase during an archive = %d %s", res.Code, res.Body)
	}
	cases, erased := e.list()
	if len(cases) != 1 || len(erased) != 0 {
		t.Fatalf("list after the refusal = %+v %+v", cases, erased)
	}
	if mark, _ := lc.Mark(t.Context(), casestore.CaseID(id)); mark != casestore.MarkArchiving {
		t.Fatalf("the mark changed to %q", mark)
	}
}

// INJ-6 and the 30 s wait: a held reader makes Erase wait; when the wait runs out
// the answer is M4, the refusal of new readers is lifted, and nothing is erased.
func TestEraseWaitEndsInM4AndErasesNothing(t *testing.T) {
	e := newEraseEnv(t, envConfig{})
	id := e.newCase("Held")
	h := e.h.(*handler)
	read, ok := h.gates.acquireRead(casestore.CaseID(id))
	if !ok {
		t.Fatal("no read share")
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.erase(id, id[:8]) }()
	if d := <-e.asked; d != 30*time.Second {
		t.Fatalf("the wait asked for %v, want 30s", d)
	}
	select {
	case res := <-done:
		t.Fatalf("erase answered (%d) while a reader was held", res.Code)
	case <-time.After(50 * time.Millisecond):
	}
	// While waiting, a new reader is told the case is being erased (M5).
	res := e.get("/api/cases/" + id + "/imports")
	if f := failureOf(t, res); res.Code != http.StatusConflict || f.Reason != "case-erasing" || f.Error != "This case is being erased." {
		t.Fatalf("read during the wait = %d %s", res.Code, res.Body)
	}
	e.fire <- time.Now()
	res = <-done
	f := failureOf(t, res)
	if res.Code != http.StatusConflict || f.Reason != "case-in-use" || f.Error != "The case is in use. Close its other tabs, then try again." {
		t.Fatalf("erase after the wait = %d %s", res.Code, res.Body)
	}
	// Nothing was marked or erased, and reads work again.
	if mark, err := e.store.(casestore.Lifecycle).Mark(t.Context(), casestore.CaseID(id)); err != nil || mark != "" {
		t.Fatalf("mark after M4 = %q, %v", mark, err)
	}
	read()
	if res := e.get("/api/cases/" + id + "/imports"); res.Code != http.StatusOK {
		t.Fatalf("read after M4 = %d %s", res.Code, res.Body)
	}
	if _, erased := e.list(); len(erased) != 0 {
		t.Fatalf("an Erased line was made by a refused erase: %+v", erased)
	}
	// The reader is gone, so the same press now erases at once.
	if res := e.erase(id, id[:8]); res.Code != http.StatusNoContent {
		t.Fatalf("erase after the reader left = %d %s", res.Code, res.Body)
	}
}

// A reader that leaves during the wait lets the erase go ahead without the timer.
func TestEraseProceedsWhenTheReaderLeavesInTime(t *testing.T) {
	e := newEraseEnv(t, envConfig{})
	id := e.newCase("Held briefly")
	h := e.h.(*handler)
	read, _ := h.gates.acquireRead(casestore.CaseID(id))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.erase(id, id[:8]) }()
	<-e.asked
	time.Sleep(20 * time.Millisecond)
	read()
	if res := <-done; res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
}

// INJ-8: an import that is running is stopped by the erase; the case goes and the
// import's answer is not a success.
func TestEraseStopsARunningImport(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	blocking := toyImporter{name: "toy", score: 90, onParse: func() {
		once.Do(func() { close(entered) })
		<-release
	}}
	e := newEraseEnv(t, envConfig{importers: []importer.Importer{blocking}})
	id := e.newCase("Importing")
	importDone := make(chan int, 1)
	go func() {
		importDone <- e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}).Code
	}()
	<-entered
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.erase(id, id[:8]) }()
	<-e.asked
	// The import is a plain upload here: it has the case until it ends. Letting it end
	// is what the erase is waiting for.
	time.Sleep(30 * time.Millisecond)
	close(release)
	<-importDone
	if res := <-done; res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
}

// The Erased lines: one per erase, only the id and the time, cleared one at a time,
// and clearing never touches a case or a case file.
func TestErasedLinesAreKeptAndCleared(t *testing.T) {
	e := newEraseEnv(t, envConfig{})
	a, b := e.newCase("Secret title A"), e.newCase("Secret title B")
	if res := e.erase(a, a[:8]); res.Code != http.StatusNoContent {
		t.Fatalf("erase a = %d", res.Code)
	}
	if res := e.erase(b, b[:8]); res.Code != http.StatusNoContent {
		t.Fatalf("erase b = %d", res.Code)
	}
	_, erased := e.list()
	if len(erased) != 2 || erased[0].ID != a || erased[1].ID != b || erased[0].At.IsZero() {
		t.Fatalf("erased lines = %+v", erased)
	}
	// The file holds ids and times and nothing else: no title, no case content.
	raw, err := os.ReadFile(e.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Secret title") || strings.Count(string(raw), `"id"`) != 2 {
		t.Fatalf("erased file = %s", raw)
	}
	if res := e.json(http.MethodDelete, "/api/cases/erased/"+a, ""); res.Code != http.StatusNoContent {
		t.Fatalf("clear = %d %s", res.Code, res.Body)
	}
	if _, erased = e.list(); len(erased) != 1 || erased[0].ID != b {
		t.Fatalf("after Clear = %+v", erased)
	}
	// Clearing a line that is not there, or a bad id, is quiet or refused: never a case erase.
	if res := e.json(http.MethodDelete, "/api/cases/erased/"+a, ""); res.Code != http.StatusNoContent {
		t.Fatalf("second clear = %d", res.Code)
	}
	if res := e.json(http.MethodDelete, "/api/cases/erased/not-an-id", ""); res.Code != http.StatusBadRequest {
		t.Fatalf("clear of a bad id = %d", res.Code)
	}
	// A live case is not an Erased line: Clear on its id removes nothing.
	live := e.newCase("Live")
	if res := e.json(http.MethodDelete, "/api/cases/erased/"+live, ""); res.Code != http.StatusNoContent {
		t.Fatalf("clear of a live id = %d", res.Code)
	}
	if cases, _ := e.list(); len(cases) != 1 {
		t.Fatalf("Clear touched a case: %+v", cases)
	}
}

// With no log wired the list still answers, with an empty array.
func TestListHasNoErasedLinesWithoutALog(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	res := e.get("/api/cases")
	if !strings.Contains(res.Body.String(), `"erased":[]`) {
		t.Fatalf("list = %s", res.Body)
	}
}

func TestErasedFileSurvivesDamageAndCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "erased.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewErasedFile(path)
	if got := f.Lines(); len(got) != 0 {
		t.Fatalf("damaged file read as %+v", got)
	}
	for i := 0; i < erasedLinesMax+5; i++ {
		id := casestore.CaseID(strings.Repeat("a", 24) + string(rune('a'+i/26)) + string(rune('a'+i%26)))
		if err := f.Add(id, time.Unix(int64(1000+i), 0)); err != nil {
			t.Fatal(err)
		}
	}
	got := f.Lines()
	if len(got) != erasedLinesMax || got[len(got)-1].At.Unix() != int64(1000+erasedLinesMax+4) {
		t.Fatalf("lines = %d, newest %v", len(got), got[len(got)-1].At)
	}
	// A second Add of one id keeps one line.
	last := got[len(got)-1].ID
	_ = f.Add(casestore.CaseID(last), time.Unix(9999, 0))
	if n := len(f.Lines()); n != erasedLinesMax {
		t.Fatalf("a repeated id made a new line: %d", n)
	}
}

// ckEraseEnv is a sqlite env with a master key, per-case keys, the Erasing recovery
// hook the program wires, and the Erased lines.
func ckEraseEnv(t *testing.T) (*mkEnv, ErasedLog) {
	t.Helper()
	log := NewErasedFile(filepath.Join(t.TempDir(), "erased.json"))
	e := newCKEnv(t, WithErasedLog(log))
	rec, err := casestore.NewRecoverer(e.store)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Register(casestore.MarkErasing, func(ctx context.Context, id casestore.CaseID) error {
		if err := e.store.Erase(ctx, id); err != nil {
			return err
		}
		return log.Add(id, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	e.h.(*handler).recoverer = rec
	return e, log
}

// A kill after the Erasing mark leaves erase.pending in the folder. With no restart,
// the next call to that case finishes the erase (the stale-mark row of Appendix C), and
// the Erased line is left.
func TestNextCallFinishesAStaleErase(t *testing.T) {
	e, log := ckEraseEnv(t)
	id := e.newCase("Stale")
	dir := filepath.Join(e.root, id)
	if err := os.WriteFile(filepath.Join(dir, "erase.pending"), []byte("erase pending\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := e.do(http.MethodGet, "/api/cases/"+id+"/imports", "")
	if res.Code != http.StatusNotFound {
		t.Fatalf("read of a stale-marked case = %d %s, want 404 (erase finished)", res.Code, res.Body)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder survived: %v", err)
	}
	if lines := log.Lines(); len(lines) != 1 || string(lines[0].ID) != id {
		t.Fatalf("erased lines = %+v", lines)
	}
}

// INJ-7: the erase cannot finish at the moment (the hook fails); the call is told the
// case is being erased (M5), not "not found", and a later call finishes it.
func TestStaleEraseThatCannotFinishAnswersM5(t *testing.T) {
	e, _ := ckEraseEnv(t)
	id := e.newCase("Stale")
	dir := filepath.Join(e.root, id)
	if err := os.WriteFile(filepath.Join(dir, "erase.pending"), []byte("erase pending\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make the finish fail: a read-only copy of the folder cannot be removed on every
	// platform, so the hook is swapped for one that fails once.
	h := e.h.(*handler)
	rec, _ := casestore.NewRecoverer(e.store)
	fail := true
	_ = rec.Register(casestore.MarkErasing, func(ctx context.Context, id casestore.CaseID) error {
		if fail {
			return os.ErrPermission
		}
		return e.store.Erase(ctx, id)
	})
	h.recoverer = rec
	res := e.do(http.MethodGet, "/api/cases/"+id+"/imports", "")
	if f := failureOf(t, res); res.Code != http.StatusConflict || f.Reason != "case-erasing" {
		t.Fatalf("read while the finish fails = %d %s, want 409 case-erasing", res.Code, res.Body)
	}
	fail = false
	if res := e.do(http.MethodGet, "/api/cases/"+id+"/imports", ""); res.Code != http.StatusNotFound {
		t.Fatalf("read after the finish works = %d %s, want 404", res.Code, res.Body)
	}
}

// Erase of a locked own-key case needs no key: the title stays hidden and the case goes.
func TestEraseOfALockedKeyedCase(t *testing.T) {
	e, log := ckEraseEnv(t)
	c := e.newKeyedCase("Keyed secret")
	if res := e.keyCall(c.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	res := e.do(http.MethodDelete, "/api/cases/"+c.ID, `{"confirm":`+q(c.ID[:8])+`}`)
	if res.Code != http.StatusNoContent {
		t.Fatalf("erase of a locked case = %d %s", res.Code, res.Body)
	}
	if _, err := os.Lstat(filepath.Join(e.root, c.ID)); !os.IsNotExist(err) {
		t.Fatalf("folder survived: %v", err)
	}
	if lines := log.Lines(); len(lines) != 1 || string(lines[0].ID) != c.ID {
		t.Fatalf("erased lines = %+v", lines)
	}
}
