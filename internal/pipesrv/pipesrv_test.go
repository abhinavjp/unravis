package pipesrv

import (
	"context"
	"errors"
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
	if w := do(Handler(echoToken(), tok), nil, "/api/cases", ""); w.Code != 403 {
		t.Fatalf("code %d", w.Code)
	}
}

func TestUIGetsOwnerTokenAndClientTokenIsDropped(t *testing.T) {
	ui := RoleUI
	w := do(Handler(echoToken(), tok), &ui, "/api/cases", "attacker")
	if w.Code != 200 || w.Body.String() != "token="+tok {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}

func TestAGNeverGetsTokenAndOnlyItsPrefixes(t *testing.T) {
	ag := RoleAG
	h := Handler(echoToken(), tok)
	for _, p := range []string{"/api/cases", "/", "/api/shutdown", "/mcp/v1", "/cli/v1"} {
		if w := do(h, &ag, p, tok); w.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, w.Code)
		}
	}
	for _, p := range []string{"/mcp/v1/x", "/cli/v1/y"} {
		if w := do(h, &ag, p, tok); w.Code != 200 || w.Body.String() != "token=" {
			t.Errorf("%s: %d %q (token must be stripped)", p, w.Code, w.Body.String())
		}
	}
}

func TestUnknownRoleRefused(t *testing.T) {
	r := Role("x")
	if w := do(Handler(echoToken(), tok), &r, "/api/x", ""); w.Code != 403 {
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
	g := Serve(p, Handler(echoToken(), tok), func(h http.Handler) *http.Server { return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second} })

	get := func(m *memListener, path string) (int, string) {
		resp, err := client(m).Get("http://127.0.0.1" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, b := get(made[0], "/api/cases"); c != 200 || b != "token="+tok {
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
	// On non-Windows pipesec.Listen is the stub: Open must report it, not panic.
	if _, err := Open(pipesec.Listen, pipesec.PeerPolicy{}); err != nil && !errors.Is(err, pipesec.ErrUnsupported) {
		t.Fatalf("unexpected: %v", err)
	}
}
