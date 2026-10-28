package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/keeper"
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

// scn1L8KeyFails wraps the real sealed list. Until fail is called it behaves exactly like it; after, Key answers with err.
type scn1L8KeyFails struct {
	BlackboxStore
	mu  sync.Mutex
	err error
}

func (s *scn1L8KeyFails) fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *scn1L8KeyFails) Key(id string) (string, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.BlackboxStore.Key(id)
}

// D-C-02: the keeper refusals of reveal-key are pinned, and none of them carries the key.
func TestScn1L8RevealKeyKeeperAnswers(t *testing.T) {
	var wrap *scn1L8KeyFails
	b := newBBEnv(t, func(d *BlackboxDeps) {
		wrap = &scn1L8KeyFails{BlackboxStore: d.Store}
		d.Store = wrap
	})
	if res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`); res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	it := b.list().Items[0]
	sealed, err := b.bbs.Key(it.ID) // the real list, not the wrapper
	if err != nil || sealed == "" {
		t.Fatalf("sealed key = %q %v", sealed, err)
	}

	cases := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"held", keeper.ErrKeeperHeld, http.StatusForbidden, "keeper-held"},
		{"stopped", keeper.ErrNotRunning, http.StatusServiceUnavailable, "keeper-stopped"},
		{"impostor", keeper.ErrImpostor, http.StatusServiceUnavailable, "keeper-impostor"},
	}
	for _, c := range cases {
		wrap.fail(c.err)
		res := b.post("/api/blackbox/"+it.ID+"/reveal-key", "")
		f := bbFail(t, res, c.status, c.reason)
		body := res.Body.String()
		if strings.Contains(body, sealed) || strings.Contains(body, "ubk-") || strings.Contains(body, "SENTINEL") {
			t.Fatalf("%s: the answer carries a key: %s", c.name, body)
		}
		if c.reason == "keeper-held" {
			if f.Error != "The key keeper holds this read key and never gives it out." {
				t.Fatalf("held error text = %q", f.Error)
			}
			if !strings.Contains(f.Next, "Ask the person who looks after this PC.") {
				t.Fatalf("held next text = %q", f.Next)
			}
		}
	}

	// With the keeper answering again, the key comes back, so the refusals above came from the keeper and not from the entry.
	wrap.fail(nil)
	if res := b.post("/api/blackbox/"+it.ID+"/reveal-key", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), sealed) {
		t.Fatalf("reveal after the keeper answers = %d %s", res.Code, res.Body)
	}
}

// D-B-51: the facts carry the length of the longest gap, so the status can show it.
func TestScn1L8FactsCarryTheLongestGapLength(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	if it := b.mustAdd(share, ""); it.Facts == nil || it.Facts.Gaps != 0 || it.Facts.LongestGapSeconds != 0 {
		t.Fatalf("a whole recording shows a gap: %+v", it.Facts)
	}
	one := [][2]time.Time{{invT0.Add(110 * time.Minute), invT0.Add(111 * time.Minute)}}
	b.world.put(`\\web-g\Blackbox`, bbRec("web-g", func(r *simRec) { r.Skip = one }))
	short := b.mustAdd(`\\web-g\Blackbox`, "")
	three := [][2]time.Time{{invT0.Add(100 * time.Minute), invT0.Add(103 * time.Minute)}}
	b.world.put(`\\web-h\Blackbox`, bbRec("web-h", func(r *simRec) { r.Skip = three }))
	long := b.mustAdd(`\\web-h\Blackbox`, "")
	if short.Facts == nil || short.Facts.Gaps != 1 || short.Facts.LongestGapSeconds <= 0 {
		t.Fatalf("short gap facts = %+v", short.Facts)
	}
	if long.Facts == nil || long.Facts.Gaps != 1 || long.Facts.LongestGapSeconds <= short.Facts.LongestGapSeconds {
		t.Fatalf("a 3 minute skip must be longer than a 1 minute skip: short %+v long %+v", short.Facts, long.Facts)
	}
	if long.Facts.LongestGapSeconds <= 120 {
		t.Fatalf("a 3 minute skip is a long gap, got %d seconds", long.Facts.LongestGapSeconds)
	}
}
