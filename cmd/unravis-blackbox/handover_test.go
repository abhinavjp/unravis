package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/keeperwire"
)

const (
	testHandoverID   = "0123456789abcdef0123456789abcdef"
	testHandoverPipe = `\\.\pipe\UnravisKeeper`
)

type handoverCall struct {
	n          int
	id, pipe   string
	answer     []byte
	answerFail error
}

func (h *handoverCall) fetch(_ context.Context, id, pipe string) ([]byte, error) {
	h.n++
	h.id, h.pipe = id, pipe
	if h.answerFail != nil {
		return nil, h.answerFail
	}
	return append([]byte(nil), h.answer...), nil
}

func TestSetupSQLSavesTheLoginFetchedFromTheKeeper(t *testing.T) {
	g, dir, _ := setupRig(t)
	h := &handoverCall{answer: []byte(loginDoc)}
	g.d.handover = h.fetch
	c, out, errs := g.do("setup-sql", "--login-handover", testHandoverID, "--keeper-pipe", testHandoverPipe, "--json")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	noLoginLeak(t, out, errs)
	if h.n != 1 || h.id != testHandoverID || h.pipe != testHandoverPipe {
		t.Fatalf("fetch calls=%d id=%q pipe=%q", h.n, h.id, h.pipe)
	}
	if !strings.Contains(out, `"loginSaved":true`) || !bbsql.HasLogin(dir) {
		t.Fatalf("answer: %s", out)
	}
	if strings.Contains(out, testHandoverID) || strings.Contains(errs, testHandoverID) {
		t.Errorf("the hand-over id was echoed: %q %q", out, errs)
	}
}

func TestSetupSQLHandoverFailureSavesNothingAndLeaksNothing(t *testing.T) {
	for name, h := range map[string]*handoverCall{
		"fetch fails":      {answerFail: errors.New("sql01 bb_reader " + loginText)},
		"answer not login": {answer: []byte(`{"server":"sql01","user":"bb_reader","password":"` + loginText + `","extra":1}`)},
		"answer empty":     {answer: []byte(`{}`)},
	} {
		t.Run(name, func(t *testing.T) {
			g, dir, _ := setupRig(t)
			g.d.handover = h.fetch
			c, out, errs := g.do("setup-sql", "--login-handover", testHandoverID, "--keeper-pipe", testHandoverPipe, "--json")
			if c == 0 || bbsql.HasLogin(dir) {
				t.Fatalf("saved or succeeded: %d %s", c, out)
			}
			noLoginLeak(t, out, errs)
			if h.n != 1 {
				t.Errorf("fetch calls=%d", h.n)
			}
		})
	}
}

func TestSetupSQLHandoverFailureHasItsOwnPlainSentence(t *testing.T) {
	g, _, _ := setupRig(t)
	g.d.handover = (&handoverCall{answerFail: errHandover}).fetch
	c, out, _ := g.do("setup-sql", "--login-handover", testHandoverID, "--keeper-pipe", testHandoverPipe, "--json")
	if c == 0 || !strings.Contains(out, reasonLoginHandover) || !strings.Contains(out, "Start the set-up again") {
		t.Fatalf("answer: %d %s", c, out)
	}
}

func TestSetupSQLHandoverWaysAreExclusiveAndAdminFirst(t *testing.T) {
	g, dir, fs := setupRig(t)
	f, _, _ := strings.Cut(fs, "|")
	h := &handoverCall{answer: []byte(loginDoc)}
	g.d.handover = h.fetch
	if err := os.WriteFile(f, []byte(loginDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	args := func(more ...string) []string {
		return append([]string{"setup-sql", "--json"}, more...)
	}
	handover := []string{"--login-handover", testHandoverID, "--keeper-pipe", testHandoverPipe}
	if c, _, _ := g.do(args(append(handover, "--login-file", f, "--login-sha256", strings.Repeat("0", 64))...)...); c == 0 {
		t.Error("ran with a file and a hand-over")
	}
	if c, _, _ := g.do(args(append(handover, "--windows-signin")...)...); c == 0 {
		t.Error("ran with Windows sign-in and a hand-over")
	}
	if c, _, _ := g.do(args("--login-handover", testHandoverID)...); c == 0 {
		t.Error("ran with a hand-over id and no pipe")
	}
	g.admin.admin = false
	if c, _, _ := g.do(args(handover...)...); c == 0 {
		t.Error("ran without administrator rights")
	}
	if h.n != 0 || bbsql.HasLogin(dir) {
		t.Errorf("the keeper was asked (%d) or a login saved before the checks passed", h.n)
	}
}

func TestSetupSQLHandoverNeverReadsALoginFile(t *testing.T) {
	g, dir, _ := setupRig(t)
	tmp := t.TempDir()
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(k, tmp)
	}
	g.d.handover = (&handoverCall{answer: []byte(loginDoc)}).fetch
	if c, out, errs := g.do("setup-sql", "--login-handover", testHandoverID, "--keeper-pipe", testHandoverPipe, "--json"); c != 0 || !bbsql.HasLogin(dir) {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	left, err := filepath.Glob(filepath.Join(tmp, "*"))
	if err != nil || len(left) != 0 {
		t.Errorf("files appeared in the temp folder: %v %v", left, err)
	}
}

func TestValidHandover(t *testing.T) {
	long := testHandoverPipe + strings.Repeat("a", 200)
	for _, tc := range []struct {
		name, id, pipe string
		ok             bool
	}{
		{"good", testHandoverID, testHandoverPipe, true},
		{"good test pipe", testHandoverID, `\\.\pipe\unravis-keeper-test_1.x`, true},
		{"short id", testHandoverID[:31], testHandoverPipe, false},
		{"long id", testHandoverID + "0", testHandoverPipe, false},
		{"upper id", strings.ToUpper(testHandoverID), testHandoverPipe, false},
		{"non hex id", strings.Repeat("g", 32), testHandoverPipe, false},
		{"empty id", "", testHandoverPipe, false},
		{"empty pipe", testHandoverID, "", false},
		{"bare prefix", testHandoverID, `\\.\pipe\`, false},
		{"other machine", testHandoverID, `\\server\pipe\UnravisKeeper`, false},
		{"file path", testHandoverID, `C:\Windows\Temp\x`, false}, // winok: only a string handed to the validator, never opened as a path
		{"path inside", testHandoverID, `\\.\pipe\a\b`, false},
		{"dot dot", testHandoverID, `\\.\pipe\..`, false},
		{"dot dot path", testHandoverID, `\\.\pipe\..\x`, false},
		{"single dot", testHandoverID, `\\.\pipe\.`, false},
		{"space", testHandoverID, `\\.\pipe\Unravis Keeper`, false},
		{"too long", testHandoverID, long, false},
	} {
		if got := validHandover(tc.id, tc.pipe); got != tc.ok {
			t.Errorf("%s: validHandover(%q, %q) = %v, want %v", tc.name, tc.id, tc.pipe, got, tc.ok)
		}
	}
}

// fakeKeeper answers one request on the other end of an in-memory connection, so fetchHandover's wire handling is tested without Windows.
type fakeKeeper struct {
	conn  net.Conn
	reply keeperwire.Response
	got   chan keeperwire.Request
}

type fakeDialer struct {
	k   *fakeKeeper
	err error
}

type fakeConn struct{ net.Conn }

func (d fakeDialer) Dial(context.Context) (keeperwire.ClientConn, error) {
	if d.err != nil {
		return nil, d.err
	}
	server, client := net.Pipe()
	d.k.conn = server
	go func() {
		defer func() { _ = server.Close() }()
		frame, err := keeperwire.ReadFrame(server)
		if err != nil {
			return
		}
		var req keeperwire.Request
		if json.Unmarshal(frame, &req) == nil {
			d.k.got <- req
		}
		_ = keeperwire.WriteJSONFrame(server, d.k.reply)
	}()
	return fakeConn{client}, nil
}

func useFakeKeeper(t *testing.T, reply keeperwire.Response, dialErr error) *fakeKeeper {
	t.Helper()
	k := &fakeKeeper{reply: reply, got: make(chan keeperwire.Request, 1)}
	old := dialKeeper
	var asked string
	dialKeeper = func(pipe string) keeperwire.Dialer { asked = pipe; return fakeDialer{k: k, err: dialErr} }
	t.Cleanup(func() {
		dialKeeper = old
		if asked != "" && asked != testHandoverPipe {
			t.Errorf("dialled %q", asked)
		}
	})
	return k
}

func TestFetchHandoverAsksTheKeeperOnceForThatIDAndReturnsTheLogin(t *testing.T) {
	k := useFakeKeeper(t, keeperwire.Response{OK: true, Result: json.RawMessage(loginDoc)}, nil)
	doc, err := fetchHandover(context.Background(), testHandoverID, testHandoverPipe)
	if err != nil || string(doc) != loginDoc {
		t.Fatalf("doc=%q err=%v", doc, err)
	}
	req := <-k.got
	var args struct{ ID string }
	if req.V != keeperwire.ProtocolVersion || req.Op != keeperwire.OpSQLHandoverTake || json.Unmarshal(req.Args, &args) != nil || args.ID != testHandoverID {
		t.Fatalf("request: %+v args=%s", req, req.Args)
	}
}

func TestFetchHandoverRefusalsAllLookTheSame(t *testing.T) {
	for name, tc := range map[string]struct {
		reply keeperwire.Response
		dial  error
	}{
		"refused":      {reply: keeperwire.Response{Error: &keeperwire.WireError{Code: "refused", Message: "that hand-over is gone"}}},
		"ok but empty": {reply: keeperwire.Response{OK: true}},
		"ok but null":  {reply: keeperwire.Response{OK: true, Result: json.RawMessage("null")}},
		"not running":  {dial: keeperwire.ErrNotRunning},
		"impostor":     {dial: keeperwire.ErrImpostor},
	} {
		t.Run(name, func(t *testing.T) {
			useFakeKeeper(t, tc.reply, tc.dial)
			doc, err := fetchHandover(context.Background(), testHandoverID, testHandoverPipe)
			if doc != nil || err != errHandover {
				t.Fatalf("doc=%q err=%v", doc, err)
			}
		})
	}
}

func TestFetchHandoverRefusesABadIDOrPipeBeforeDialling(t *testing.T) {
	dialled := false
	old := dialKeeper
	dialKeeper = func(string) keeperwire.Dialer {
		dialled = true
		return fakeDialer{k: &fakeKeeper{}, err: errors.New("no")}
	}
	t.Cleanup(func() { dialKeeper = old })
	for _, tc := range [][2]string{{"short", testHandoverPipe}, {testHandoverID, `\\other\pipe\x`}} {
		if _, err := fetchHandover(context.Background(), tc[0], tc[1]); err != errHandover {
			t.Errorf("%v: %v", tc, err)
		}
	}
	if dialled {
		t.Error("dialled with a bad id or pipe")
	}
}
