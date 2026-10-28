package server

import (
	"net/http"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// Scenario 1 defect tests for lane L8 (Blackbox). Helpers (newBBEnv, bbRec, bbFail, jb, invT0) are in blackbox_test.go.

// D-B-42: a stopped Blackbox on this PC is Partly ready, in the list and in the status view, and it is the word state.go uses.
func TestScn1L8StoppedBlackboxIsPartlyReady(t *testing.T) {
	b := newBBEnv(t, nil)
	if res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`); res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	id := b.list().Items[0].ID
	if res := b.post("/api/blackbox/this-pc/stop", ""); res.Code != http.StatusOK {
		t.Fatalf("stop = %d %s", res.Code, res.Body)
	}
	got := b.list().Items[0].State
	if got.State != blackbox.SStopped {
		t.Fatalf("state = %+v", got)
	}
	if got.Readiness != blackbox.WordPartlyReady {
		t.Fatalf("readiness after stop = %q, want Partly ready", got.Readiness)
	}
	if blackbox.WordPartlyReady != "Partly ready" {
		t.Fatalf("fixed word changed: %q", blackbox.WordPartlyReady)
	}
	if want := stoppedHere().Readiness; got.Readiness != want {
		t.Fatalf("list says %q but stoppedHere says %q", got.Readiness, want)
	}
	// The check route reads the same state, so it says the same thing.
	res := b.post("/api/blackbox/"+id+"/check", "")
	if res.Code != http.StatusOK {
		t.Fatalf("check = %d %s", res.Code, res.Body)
	}
	var one struct {
		State blackbox.StateInfo `json:"state"`
	}
	decodeBody(t, res, &one)
	if one.State.Readiness != "Partly ready" {
		t.Fatalf("check readiness = %q", one.State.Readiness)
	}
}
