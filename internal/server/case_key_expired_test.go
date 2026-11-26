package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
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
