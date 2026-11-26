package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
)

// ckClock is a store clock the test moves.
type ckClock struct{ at atomic.Int64 }

func (c *ckClock) set(t time.Time) { c.at.Store(t.UnixNano()) }
func (c *ckClock) now() time.Time  { return time.Unix(0, c.at.Load()) }
func (e *mkEnv) listed(id string) bool {
	e.t.Helper()
	infos, err := e.store.List(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, i := range infos {
		if string(i.ID) == id {
			return true
		}
	}
	return false
}

// A case with its own key that was closed over 30 days ago and locked since is not seen by the sweep (it has no close date in the
// listing while locked). Unlocking it applies the sweep rule at once; one that is not yet due stays (RV-DAT-8).
func TestCaseKeyUnlockErasesACaseThatExpiredWhileLocked(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	old := e.newKeyedCase("old and closed")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	clock.set(time.Now().Add(-time.Hour))
	fresh := e.newKeyedCase("closed an hour ago")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(fresh.ID)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old.ID, fresh.ID} {
		if res := e.keyCall(id, "lock", `{}`); res.Code != http.StatusOK {
			t.Fatalf("lock = %d %s", res.Code, res.Body)
		}
	}
	if gone, err := e.store.Sweep(context.Background(), time.Now()); err != nil || len(gone) != 0 {
		t.Fatalf("the sweep saw a locked case: %v, %v", gone, err)
	}

	if res := e.keyCall(fresh.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("unlock fresh = %d %s", res.Code, res.Body)
	}
	if !e.listed(fresh.ID) {
		t.Fatal("a case closed an hour ago was erased at unlock")
	}
	if res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("unlock old = %d %s", res.Code, res.Body)
	}
	if e.listed(old.ID) {
		t.Fatal("a case closed over 30 days ago is still there after its unlock")
	}
	if res := e.do(http.MethodGet, "/api/cases/"+old.ID, ""); res.Code != http.StatusNotFound {
		t.Fatalf("open the erased case = %d %s, want 404", res.Code, res.Body)
	}
}

// A wrong key reads nothing and erases nothing.
func TestCaseKeyWrongKeyDoesNotEraseAnExpiredCase(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	old := e.newKeyedCase("old and closed")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	if res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass2)+`}`); res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d %s", res.Code, res.Body)
	}
	if !e.listed(old.ID) {
		t.Fatal("a wrong key erased the case")
	}
}

// An unlock that erased the case says so (erased: true and when it was closed): a reply the page reads, not an empty 200
// followed by a "case not found" page. The erase leaves the "case_erased" event and exactly one Erased line.
func TestCaseKeyUnlockReplyNamesTheErase(t *testing.T) {
	clock := &ckClock{}
	closedAt := time.Now().Add(-casestore.Retention - 48*time.Hour)
	clock.set(closedAt)
	var events bytes.Buffer
	erasedLog := NewErasedFile(filepath.Join(t.TempDir(), "erased.json"))
	e := newCKEnvClock(t, clock.now, WithLogger(bufLogger(&events)), WithErasedLog(erasedLog))
	old := e.newKeyedCase("old and closed")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || !out.Erased || out.OwnKey || out.Locked {
		t.Fatalf("unlock of an expired case = %d %+v, want 200 with erased true", res.Code, out)
	}
	if out.ClosedAt == nil || out.ClosedAt.Sub(closedAt).Abs() > 2*time.Second {
		t.Errorf("closedAt = %v, want about %v", out.ClosedAt, closedAt)
	}
	if got := events.String(); !strings.Contains(got, "case_erased") || !strings.Contains(got, "expired_unlock") {
		t.Errorf("no case_erased event for the unlock erase: %s", got)
	}
	if lines := erasedLog.Lines(); len(lines) != 1 || string(lines[0].ID) != old.ID {
		t.Errorf("Erased lines = %+v, want exactly one for the case", lines)
	}
}

// A close date in the future (a clock that was set forward when the case was closed) is never "over 30 days ago": nothing is erased.
func TestCaseKeyUnlockKeepsACaseClosedInTheFuture(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(casestore.Retention + 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	c := e.newKeyedCase("closed by a clock that ran ahead")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(c.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(c.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	res := e.keyCall(c.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Erased || !e.listed(c.ID) {
		t.Fatalf("unlock = %d %+v, listed %v, want the case kept", res.Code, out, e.listed(c.ID))
	}
}

// A running archive of the case is never cut short: the unlock keeps the case (the manual erase refuses the same way).
func TestCaseKeyUnlockKeepsAnExpiredCaseWhileItsArchiveRuns(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	old := e.newKeyedCase("old, closed, archive running")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.h.(*handler).caseFiles().jobs.put(newFileJob(jobArchive, casestore.CaseID(old.ID), cancel))
	res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Erased || !out.OwnKey {
		t.Fatalf("unlock = %d %+v, want a normal unlocked answer", res.Code, out)
	}
	if !e.listed(old.ID) {
		t.Fatal("a case with a running archive was erased at unlock")
	}
}

// The case is reopened while the unlock waits for its slot: the close date is gone, so the case is kept.
func TestCaseKeyUnlockKeepsACaseReopenedWhileItWaits(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	var e *mkEnv
	var oldID string
	e = newCKEnvClock(t, clock.now, withEraseTimer(func(d time.Duration) <-chan time.Time {
		// The timer is made after the first expiry check and before the slot is taken: reopen the case here.
		if err := e.store.ReopenCase(context.Background(), casestore.CaseID(oldID)); err != nil {
			t.Errorf("reopen: %v", err)
		}
		return time.After(d)
	}))
	old := e.newKeyedCase("old, closed, then reopened")
	oldID = old.ID
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Erased || !out.OwnKey {
		t.Fatalf("unlock = %d %+v, want a normal unlocked answer", res.Code, out)
	}
	if !e.listed(old.ID) {
		t.Fatal("a case reopened during the unlock was erased")
	}
}

// listsArchived shows one case as an archived one, as the listing of an archived own-key case does once it is unlocked.
type listsArchived struct {
	*sqlite.Store
	id casestore.CaseID
}

func (l *listsArchived) List(ctx context.Context) ([]casestore.CaseInfo, error) {
	items, err := l.Store.List(ctx)
	for i := range items {
		if items[i].ID == l.id {
			items[i].Archive = &casestore.ArchiveInfo{Active: true}
		}
	}
	return items, err
}

// An archived case is never erased by the sweep, so unlocking an old archived own-key case keeps it too.
func TestCaseKeyUnlockKeepsAnExpiredArchivedCase(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	old := e.newKeyedCase("old, closed and archived")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	e.h.(*handler).cases = &listsArchived{Store: e.store, id: casestore.CaseID(old.ID)}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Erased || !out.OwnKey {
		t.Fatalf("unlock of an archived case = %d %+v", res.Code, out)
	}
	if !e.listed(old.ID) {
		t.Fatal("an archived case was erased at unlock")
	}
}

// eraseFails refuses to erase, as a store that cannot remove the folder does.
type eraseFails struct{ *sqlite.Store }

func (eraseFails) Erase(context.Context, casestore.CaseID) error { return os.ErrPermission }

// An erase that fails leaves the case, unlocked and readable; the next start sweeps it. The unlock itself still worked.
func TestCaseKeyUnlockKeepsTheCaseWhenTheEraseFails(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	e := newCKEnvClock(t, clock.now)
	old := e.newKeyedCase("old and closed")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	if res := e.keyCall(old.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	e.h.(*handler).cases = eraseFails{e.store}
	res := e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`)
	var out caseKeyDTO
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Erased || !out.OwnKey || out.Locked {
		t.Fatalf("unlock with a failing erase = %d %+v, want a normal unlocked answer", res.Code, out)
	}
	if !e.listed(old.ID) {
		t.Fatal("the case is gone although the erase failed")
	}
}

// While the unlock waits for the case to be free it does not hold the key lock: another case's key is answered at once.
// When the wait runs out the case stays, unlocked, for the next start's sweep.
func TestCaseKeyUnlockWaitingForTheGateHoldsNoKeyLockAndKeepsTheCase(t *testing.T) {
	clock := &ckClock{}
	clock.set(time.Now().Add(-casestore.Retention - 48*time.Hour))
	fire := make(chan time.Time)
	asked := make(chan time.Duration, 4)
	e := newCKEnvClock(t, clock.now, withEraseTimer(func(d time.Duration) <-chan time.Time {
		asked <- d
		return fire
	}))
	old := e.newKeyedCase("old and closed")
	other := e.newKeyedCase("another case")
	if err := e.store.CloseCase(context.Background(), casestore.CaseID(old.ID)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old.ID, other.ID} {
		if res := e.keyCall(id, "lock", `{}`); res.Code != http.StatusOK {
			t.Fatalf("lock = %d %s", res.Code, res.Body)
		}
	}
	// Someone is reading the old case: the erase has to wait for the case.
	busy, err := e.h.(*handler).gates.acquire(context.Background(), casestore.CaseID(old.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer busy()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.keyCall(old.ID, "unlock", `{"passphrase":`+q(ckPass)+`}`) }()
	select {
	case d := <-asked:
		if d <= 0 || d > 10*time.Second {
			t.Fatalf("the unlock waits %v for the case, want a few seconds", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the unlock never tried to erase")
	}
	otherCode := make(chan int, 1)
	go func() { otherCode <- e.keyCall(other.ID, "unlock", `{"passphrase":`+q(ckPass2)+`}`).Code }()
	select {
	case code := <-otherCode:
		if code != http.StatusUnauthorized {
			t.Fatalf("another case's wrong key = %d, want 401", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another case's key call was held back while the unlock waited for the case")
	}
	close(fire) // the wait runs out
	select {
	case res := <-done:
		var out caseKeyDTO
		decodeBody(t, res, &out)
		if res.Code != http.StatusOK || out.Erased || !out.OwnKey || out.Locked {
			t.Fatalf("unlock after the wait = %d %+v, want a normal unlocked answer", res.Code, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the unlock did not finish after the wait ran out")
	}
	if !e.listed(old.ID) {
		t.Fatal("the case was erased although the wait ran out")
	}
}
