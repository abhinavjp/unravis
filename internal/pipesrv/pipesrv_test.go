package pipesrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/pipesec"
)

const tok = "tok-0123456789abcdef0123456789abcdef"

func echoToken() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, //nolint:gosec // test echo of a fixed token into a recorder
			"token="+r.Header.Get(tokenHeader))
	})
}

func do(h http.Handler, role *Role, path, sentToken string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if sentToken != "" {
		r.Header.Set(tokenHeader, sentToken)
	}
	if role != nil {
		r = r.WithContext(WithRole(r.Context(), *role))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNoRoleIsRefused(t *testing.T) {
	if w := do(Handler(echoToken()), nil, "/api/cases", ""); w.Code != 403 {
		t.Fatalf("code %d", w.Code)
	}
}

func TestUITokenPassesThroughUnchanged(t *testing.T) {
	ui := RoleUI
	h := Handler(echoToken())
	if w := do(h, &ui, "/api/cases", "client-token"); w.Code != 200 || w.Body.String() != "token=client-token" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	// No token from the client means none reaches the handler, which then refuses it itself (BL-303 TestShutdownRequiresToken).
	if w := do(h, &ui, "/api/shutdown", ""); w.Body.String() != "token=" {
		t.Fatalf("%q: the pipe must not add a token", w.Body.String())
	}
}

func TestAGOnlyBridgeRoutesAndPrefixes(t *testing.T) {
	ag := RoleAG
	h := Handler(echoToken())
	for _, p := range []string{"/api/cases", "/", "/api/shutdown", "/mcp/v1", "/cli/v1", "/api/info/x", "/mcp/v1/../api/cases", "/mcp/v1//x", "/api/keeper/x"} {
		if w := do(h, &ag, p, tok); w.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, w.Code)
		}
	}
	for _, p := range []string{"/mcp/v1/x", "/cli/v1/y", "/api/info", "/api/sources", "/api/analyze", "/api/requests", "/api/activity", "/api/slowest", "/api/breakdown", "/api/session-proof"} {
		if w := do(h, &ag, p, tok); w.Code != 200 || w.Body.String() != "token="+tok {
			t.Errorf("%s: %d %q (the client's token passes unchanged)", p, w.Code, w.Body.String())
		}
	}
}

func TestUnknownRoleRefused(t *testing.T) {
	r := Role("x")
	if w := do(Handler(echoToken()), &r, "/api/x", ""); w.Code != 403 {
		t.Fatalf("code %d", w.Code)
	}
}

// memListener is an in-memory listener: Dial returns the client end of a pipe the server accepts.
type memListener struct {
	ch   chan net.Conn
	done chan struct{}
	addr net.Addr
}

func newMem() *memListener {
	return &memListener{ch: make(chan net.Conn), done: make(chan struct{}), addr: pipeAddr("mem")}
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

func (m *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-m.ch:
		return c, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}
func (m *memListener) Close() error {
	select {
	case <-m.done:
	default:
		close(m.done)
	}
	return nil
}
func (m *memListener) Addr() net.Addr { return m.addr }
func (m *memListener) dial() net.Conn {
	c, s := net.Pipe()
	m.ch <- s
	return c
}

func fakeListen(made *[]*memListener, names *[]string) ListenFunc {
	return func(cfg pipesec.ListenConfig) (net.Listener, error) {
		m := newMem()
		*made = append(*made, m)
		*names = append(*names, cfg.Name)
		return m, nil
	}
}

func client(m *memListener) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return m.dial(), nil }}}
}

func TestServeRolesPerPipeAndShutdown(t *testing.T) {
	var made []*memListener
	var names []string
	p, err := Open(fakeListen(&made, &names), pipesec.PeerPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.UIName, `\\.\pipe\unravis.ui.`) || !strings.HasPrefix(p.AGName, `\\.\pipe\unravis.ag.`) || p.UIName == p.AGName {
		t.Fatalf("names %q %q", p.UIName, p.AGName)
	}
	g := Serve(p, Handler(echoToken()), func(h http.Handler) *http.Server { return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second} })

	get := func(m *memListener, path string) (int, string) {
		resp, err := client(m).Get("http://127.0.0.1" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, b := get(made[0], "/api/cases"); c != 200 || b != "token=" {
		t.Fatalf("ui: %d %q", c, b)
	}
	if c, _ := get(made[1], "/api/cases"); c != 404 {
		t.Fatalf("ag GUI route: %d", c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-g.Done(); !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("done: %v", err)
		}
	}
}

func TestOpenClosesFirstPipeWhenSecondFails(t *testing.T) {
	var first *memListener
	n := 0
	listen := func(cfg pipesec.ListenConfig) (net.Listener, error) {
		n++
		if n == 2 {
			return nil, errors.New("boom")
		}
		first = newMem()
		return first, nil
	}
	if _, err := Open(listen, pipesec.PeerPolicy{}); err == nil {
		t.Fatal("want error")
	}
	select {
	case <-first.done:
	default:
		t.Fatal("first pipe left open")
	}
}

func TestOpenOnThisOSUsesPipesec(t *testing.T) {
	// On non-Windows pipesec.Listen is the stub: Open must report it, not panic. On Windows it succeeds and the pipes are closed.
	p, err := Open(pipesec.Listen, pipesec.PeerPolicy{})
	if err == nil {
		_ = p.Close()
	} else if !errors.Is(err, pipesec.ErrUnsupported) {
		t.Fatalf("unexpected: %v", err)
	}
}

type peerConn struct{ net.Conn }

func (peerConn) Peer() pipesec.Peer {
	return pipesec.Peer{PID: 4242, Image: `C:\Unravis\unravis.exe`, LogonSID: "S-1-5-5-0-1234567890", UserSID: "S-1-5-21-secret-1001"}
}

// TestPipeStatsPeerPIDLogged: a connection logs the peer PID, image and the last 8 characters of the logon SID, and nothing else.
func TestPipeStatsPeerPIDLogged(t *testing.T) {
	var got []string
	old := logf
	logf = func(f string, a ...any) { got = append(got, fmt.Sprintf(f, a...)) }
	t.Cleanup(func() { logf = old })
	c, s := net.Pipe()
	defer func() { _ = c.Close(); _ = s.Close() }()
	logPeer(RoleUI, peerConn{s})
	logPeer(RoleAG, s) // no peer info: nothing logged
	if len(got) != 1 {
		t.Fatalf("lines %q", got)
	}
	for _, want := range []string{"pid=4242", `unravis.exe`, "...34567890", "ui"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("%q lacks %q", got[0], want)
		}
	}
	for _, bad := range []string{"1234567890", "secret", "S-1-5"} {
		if strings.Contains(got[0], bad) {
			t.Errorf("%q leaks %q", got[0], bad)
		}
	}
}
