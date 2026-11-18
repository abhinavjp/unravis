package server

import (
	"net/http"
	"strings"
	"testing"
)

// SEC-10: an install that listens beyond this PC says so; a loopback one does not.
func TestBlackboxInstallWarnsWhenListeningBeyondThisPC(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "bindWarning") {
		t.Fatalf("a loopback install carries a warning: %s", res.Body)
	}

	c := newBBEnv(t, nil)
	res = c.post("/api/blackbox/this-pc/install", `{"name":"web-a","bind":"0.0.0.0"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	var out struct {
		BindWarning string `json:"bindWarning"`
	}
	decodeBody(t, res, &out)
	if !strings.Contains(out.BindWarning, "Firewall") {
		t.Fatalf("no warning in %s", res.Body)
	}
	if strings.Contains(res.Body.String(), "ubk-") {
		t.Fatalf("the answer carries the key: %s", res.Body)
	}
}

// SEC-18: showing the read key is rate limited, like the other routes that test a connection.
func TestBlackboxRevealKeyIsRateLimited(t *testing.T) {
	b := newBBEnv(t, nil)
	if res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`); res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	it := b.list().Items[0]
	limited := false
	for i := 0; i < 100; i++ {
		res := b.post("/api/blackbox/"+it.ID+"/reveal-key", "")
		if res.Code == http.StatusTooManyRequests {
			limited = true
			f := bbFail(t, res, http.StatusTooManyRequests, "rate")
			if f.Next == "" || strings.Contains(res.Body.String(), "ubk-") {
				t.Fatalf("limit answer: %s", res.Body)
			}
			break
		}
		if res.Code != http.StatusOK {
			t.Fatalf("reveal %d = %d %s", i, res.Code, res.Body)
		}
	}
	if !limited {
		t.Fatal("the key could be asked for without limit")
	}
}

// SEC-23: the download name is quoted by the standard library, not by hand.
func TestBlackboxPackDownloadNameIsQuotedSafely(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/pack", `{"name":"web-b"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("pack = %d %s", res.Code, res.Body)
	}
	cd := res.Header().Get("Content-Disposition")
	if cd != `attachment; filename=unravis-blackbox-web-b.zip` && cd != `attachment; filename="unravis-blackbox-web-b.zip"` {
		t.Fatalf("disposition %q", cd)
	}
}

// SEC-FIX-13: the reveal-key budget is its own: probing addresses cannot lock the
// owner out of "show key", and it is tight because the key is a secret.
func TestBlackboxRevealKeyHasItsOwnBudget(t *testing.T) {
	hub := newBlackboxHub(BlackboxDeps{})
	for hub.probes.allow() { // the probe budget is used up
	}
	if !hub.reveals.allow() {
		t.Fatal("showing the key was refused because the probe budget was used up")
	}
	if hub.reveals.limit > 10 || hub.reveals.limit >= hub.probes.limit {
		t.Fatalf("the reveal limit %d per minute is loose for a secret", hub.reveals.limit)
	}
	n := 1
	for hub.reveals.allow() {
		n++
	}
	if n != hub.reveals.limit {
		t.Fatalf("%d reveals allowed, limit is %d", n, hub.reveals.limit)
	}
}
