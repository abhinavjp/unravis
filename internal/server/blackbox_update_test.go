package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// Tests of the "Update the recorder" route and of the update fields in the list.
// The world is the in-memory one of blackbox_demo.go: no service, program or
// administrator prompt is touched. The real replace is tested in internal/blackbox.

const bbUpdatePath = "/api/blackbox/this-pc/update"

func TestBlackboxUpdateAnswersForAnInstalledBlackbox(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	res := b.post(bbUpdatePath, "")
	if res.Code != http.StatusOK {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}
	var out struct {
		OK      bool `json:"ok"`
		Updated bool `json:"updated"`
	}
	decodeBody(t, res, &out)
	if !out.OK || out.Updated {
		t.Fatalf("answer = %+v (the pretend recorder is already current)", out)
	}
	if got := b.pc.callLog(); len(got) == 0 || got[len(got)-1] != "update" {
		t.Fatalf("calls = %v", got)
	}
	if l := b.list(); !l.ThisPC.Installed || l.Items[0].State.State != blackbox.SRunning {
		t.Fatalf("an update that changed nothing disturbed the list: %+v", l)
	}
}

func TestBlackboxUpdateWithNothingInstalledIsAPlainFailure(t *testing.T) {
	b := newBBEnv(t, nil)
	f := bbFail(t, b.post(bbUpdatePath, ""), http.StatusUnprocessableEntity, string(blackbox.ReasonNotInstall))
	if f.What == "" || f.Next == "" {
		t.Fatalf("no words: %+v", f)
	}
}

func TestBlackboxUpdateFailureWords(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{&blackbox.ThisPCError{Reason: string(blackbox.ReasonUpdateFailed), Message: "The updated recorder did not start. The previous program was put back and the recorder is running with it again."}, http.StatusUnprocessableEntity, string(blackbox.ReasonUpdateFailed)},
		{&blackbox.ThisPCError{Reason: blackbox.ReasonDeclined, Message: "Windows asked for permission and it was declined."}, http.StatusUnprocessableEntity, blackbox.ReasonDeclined},
		{&blackbox.ThisPCError{Reason: string(blackbox.ReasonNoAdmin), Message: "This account is not an administrator."}, http.StatusUnprocessableEntity, string(blackbox.ReasonNoAdmin)},
		{errors.New(`C:\secret\path exploded`), http.StatusInternalServerError, "other"},
	}
	for _, c := range cases {
		b.pc.failNext = c.err
		f := bbFail(t, b.post(bbUpdatePath, ""), c.code, c.reason)
		if f.What == "" || f.Next == "" {
			t.Fatalf("%s: no words: %+v", c.reason, f)
		}
		if strings.Contains(f.Error+f.What+f.Next, "secret") {
			t.Fatalf("an unknown error leaked its text: %+v", f)
		}
	}
	// A failed update leaves the installed Blackbox listed and running.
	if l := b.list(); !l.ThisPC.Installed || len(l.Items) != 1 {
		t.Fatalf("list = %+v", l)
	}
}

func TestBlackboxUpdateFailureKeepsItsOwnSentence(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	msg := "The updated recorder did not start. The previous program was put back and the recorder is running with it again."
	b.pc.failNext = &blackbox.ThisPCError{Reason: string(blackbox.ReasonUpdateFailed), Message: msg}
	f := bbFail(t, b.post(bbUpdatePath, ""), http.StatusUnprocessableEntity, string(blackbox.ReasonUpdateFailed))
	if !strings.Contains(f.What, "put back") {
		t.Fatalf("the rollback sentence was lost from the words: %+v", f)
	}
}

func TestBlackboxUpdateIsOneChangeAtATime(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	b.pc.entered = make(chan struct{}, 1)
	b.pc.hold = make(chan struct{})
	done := make(chan int, 1)
	go func() { done <- b.post(bbUpdatePath, "").Code }()
	select {
	case <-b.pc.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the update never started")
	}
	bbFail(t, b.post("/api/blackbox/this-pc/stop", ""), http.StatusConflict, "busy-here")
	bbFail(t, b.post(bbUpdatePath, ""), http.StatusConflict, "busy-here")
	close(b.pc.hold)
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("the first update = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the update never finished")
	}
	for _, c := range b.pc.callLog() {
		if c == "stop" {
			t.Fatal("the refused press still reached the helper")
		}
	}
}

func TestBlackboxUpdateRefusesTheWrongMethodAndNoToken(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if res := b.e.json(m, bbUpdatePath, ""); res.Code != http.StatusMethodNotAllowed && res.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", m, res.Code)
		}
	}
	if res := request(b.e.h, http.MethodPost, bbUpdatePath, "", false); res.Code == http.StatusOK {
		t.Fatalf("an unauthenticated update answered: %d", res.Code)
	}
	for _, c := range b.pc.callLog() {
		if c == "update" {
			t.Fatal("a refused request still reached the helper")
		}
	}
}

func TestBlackboxListSaysWhenTheRecorderIsOlder(t *testing.T) {
	cases := []struct {
		name    string
		program func() string // nil: the server cannot tell
		want    bool
		show    string
	}{
		{"older", func() string { return "1.0.0" }, true, "1.0.0"},
		{"same", func() string { return "0.9.0" }, false, "0.9.0"},
		{"local build ships", func() string { return "dev" }, true, "dev"},
		{"unknown program", func() string { return "" }, false, ""},
		{"no way to ask", nil, false, ""},
	}
	for _, c := range cases {
		b := newBBEnv(t, func(d *BlackboxDeps) { d.ProgramVersion = c.program })
		b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
		l := b.list()
		if l.ThisPC.UpdateAvailable != c.want || l.ThisPC.ProgramVersion != c.show {
			t.Fatalf("%s: this PC = %+v", c.name, l.ThisPC)
		}
		if c.program != nil && c.show != "" && l.ThisPC.RecorderVersion != "0.9.0" {
			t.Fatalf("%s: recorder version = %q", c.name, l.ThisPC.RecorderVersion)
		}
	}
}

func TestBlackboxListMakesNoUpdateClaimWithoutAnInstalledBlackbox(t *testing.T) {
	b := newBBEnv(t, func(d *BlackboxDeps) { d.ProgramVersion = func() string { return "9.9.9" } })
	if l := b.list(); l.ThisPC.UpdateAvailable || l.ThisPC.ProgramVersion != "" {
		t.Fatalf("this PC = %+v", l.ThisPC)
	}
}
