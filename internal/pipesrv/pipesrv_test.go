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
	for _, p := range []string{"/api/cases", "/", "/api/shutdown", "/mcp/v1", "/cli/v1", "/api/info/x", "/mcp/v1/../api/cases", "/mcp/v1//x", "/api/keeper/x",
		// BL-347: the person's approval routes are never reachable by a host (the ag pipe), whatever the token
		"/api/mcp/profiles", "/api/mcp/profiles/claude/approve", "/api/mcp/stop", "/api/mcp/resume"} {
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

// shutdownReq posts (or uses method) to the shutdown route with an optional token.
func shutdownReq(h http.Handler, role *Role, method, sentToken string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, ShutdownPath, nil)
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

// TestShutdownRouteOnlyOnUIPipe: the ag pipe answers 404 (even with the right token), and a handler built without WithShutdown, which is
// what loopback serves, does not have the route either; the served handler's own answer (here 200 from the echo) is not a shutdown.
func TestShutdownRouteOnlyOnUIPipe(t *testing.T) {
	var stops int
	h := Handler(echoToken(), WithShutdown(tok, func() { stops++ }))
	ag := RoleAG
	if w := shutdownReq(h, &ag, "POST", tok); w.Code != 404 {
		t.Fatalf("ag pipe: %d, want 404", w.Code)
	}
	if w := shutdownReq(h, nil, "POST", tok); w.Code != 403 {
		t.Fatalf("no role: %d, want 403", w.Code)
	}
	bare := Handler(echoToken())
	ui := RoleUI
	if w := shutdownReq(bare, &ui, "POST", tok); w.Body.String() != "token="+tok {
		t.Fatalf("without WithShutdown the request must reach the served handler untouched: %d %q", w.Code, w.Body.String())
	}
	if stops != 0 {
		t.Fatalf("stop ran %d times without a valid ui request", stops)
	}
}

func TestShutdownRequiresToken(t *testing.T) {
	var stops int
	h := Handler(echoToken(), WithShutdown(tok, func() { stops++ }))
	ui := RoleUI
	for name, sent := range map[string]string{"none": "", "wrong": "x" + tok[1:], "short": tok[:5], "long": tok + "0"} {
		if w := shutdownReq(h, &ui, "POST", sent); w.Code != 401 {
			t.Errorf("%s token: %d, want 401", name, w.Code)
		}
	}
	if stops != 0 {
		t.Fatalf("stop ran %d times without the token", stops)
	}
	// An empty configured token never matches, not even an empty header.
	if w := shutdownReq(Handler(echoToken(), WithShutdown("", func() { stops++ })), &ui, "POST", ""); w.Code != 401 || stops != 0 {
		t.Fatalf("empty configured token: %d, stops %d", w.Code, stops)
	}
}

func TestShutdownMethodAndOrigin(t *testing.T) {
	var stops int
	h := Handler(echoToken(), WithShutdown(tok, func() { stops++ }))
	ui := RoleUI
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		if w := shutdownReq(h, &ui, m, tok); w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Errorf("%s: %d allow %q, want 405 POST", m, w.Code, w.Header().Get("Allow"))
		}
	}
	r := httptest.NewRequest("POST", ShutdownPath, nil)
	r.Header.Set(tokenHeader, tok)
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(WithRole(r.Context(), RoleUI)))
	if w.Code != 403 || stops != 0 {
		t.Fatalf("origin: %d stops %d, want 403 and no stop", w.Code, stops)
	}
}

func TestShutdownAcceptedStopsOnce(t *testing.T) {
	var stops int
	h := Handler(echoToken(), WithShutdown(tok, func() { stops++ }))
	ui := RoleUI
	w := shutdownReq(h, &ui, "POST", tok)
	if w.Code != 202 || strings.TrimSpace(w.Body.String()) != `{"state":"stopping"}` || stops != 1 {
		t.Fatalf("%d %q stops %d, want 202 stopping and one stop", w.Code, w.Body.String(), stops)
	}
}

func TestHelloOnlyOnUIPipeAndOnlyGet(t *testing.T) {
	h := Handler(echoToken(), WithShutdown(tok, func() {}))
	ui, ag := RoleUI, RoleAG
	req := func(role *Role, method, origin string) *httptest.ResponseRecorder {
		image := `C:\Program Files\Unravis\unravis-desktop.exe`
		r := httptest.NewRequest(method, HelloPath, nil)
		r = r.WithContext(WithPeerImage(WithRole(r.Context(), *role), image))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req(&ui, "GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), tok) {
		t.Fatalf("ui GET: %d %q", w.Code, w.Body.String())
	}
	if w := req(&ui, "POST", ""); w.Code != 405 {
		t.Errorf("POST: %d", w.Code)
	}
	if w := req(&ui, "GET", "http://x"); w.Code != 403 {
		t.Errorf("with Origin: %d", w.Code)
	}
	if w := req(&ag, "GET", ""); w.Code != 404 {
		t.Errorf("ag: %d, want 404", w.Code)
	}
	// The CLI and the MCP bridge may use the ui pipe, but they do not get the token from hello; neither does an unknown peer.
	for _, img := range []string{`C:\Program Files\Unravis\unravis.exe`, `C:\x\notunravis-desktop.exe`, ""} {
		r := httptest.NewRequest("GET", HelloPath, nil)
		r = r.WithContext(WithPeerImage(WithRole(r.Context(), RoleUI), img))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || strings.Contains(w.Body.String(), tok) {
			t.Errorf("image %q: %d %q, want 403 and no token", img, w.Code, w.Body.String())
		}
	}
	// No token configured: the route is off and the request falls through to the served handler.
	off := Handler(echoToken())
	r := httptest.NewRequest("GET", HelloPath, nil)
	r = r.WithContext(WithRole(r.Context(), RoleUI))
	w := httptest.NewRecorder()
	off.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), `"token"`) {
		t.Errorf("hello answered with no token configured: %q", w.Body.String())
	}
}

func TestCLIRoutesNeedACLIImageAndOnlyOnAG(t *testing.T) {
	cli := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "cli:"+r.Header.Get(tokenHeader)) //nolint:gosec // test echo into a recorder
	})
	h := Handler(echoToken(), WithCLI(cli))

	req := func(role Role, image, p string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", p, nil)
		r = r.WithContext(WithPeerImage(WithRole(r.Context(), role), image))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req(RoleAG, `C:\App\bin\unravis.exe`, "/cli/v1/op"); w.Code != 200 || w.Body.String() != "cli:" {
		t.Errorf("the command line image: %d %q", w.Code, w.Body.String())
	}
	if w := req(RoleAG, `C:\App\unravis-desktop.exe`, "/cli/v1/op"); w.Code != 403 {
		t.Errorf("the shell image on a cli route: %d", w.Code)
	}
	if w := req(RoleAG, "", "/cli/v1/op"); w.Code != 403 {
		t.Errorf("no image: %d", w.Code)
	}
	if w := req(RoleAG, `C:\App\bin\unravis.exe`, "/cli/v1/../api/cases"); w.Code != 404 {
		t.Errorf("an unclean path: %d", w.Code)
	}
	// the ui pipe never reaches the cli handler
	if w := req(RoleUI, `C:\App\bin\unravis.exe`, "/cli/v1/op"); strings.HasPrefix(w.Body.String(), "cli:") {
		t.Errorf("the ui pipe reached the cli handler")
	}
	// without the option the prefix goes to the served handler, as before
	plain := Handler(echoToken())
	r := httptest.NewRequest("GET", "/cli/v1/op", nil)
	r = r.WithContext(WithPeerImage(WithRole(r.Context(), RoleAG), `C:\App\bin\unravis.exe`))
	w := httptest.NewRecorder()
	plain.ServeHTTP(w, r)
	if strings.HasPrefix(w.Body.String(), "cli:") {
		t.Errorf("no option, but the cli handler answered")
	}
}

func TestMCPRoutesNeedAnMCPImageAndOnlyOnAG(t *testing.T) {
	var reached string
	mcpH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.URL.Path; _, _ = io.WriteString(w, "mcp") })
	h := Handler(echoToken(), WithMCP(mcpH))
	req := func(role Role, image, p string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", p, nil)
		r = r.WithContext(WithPeerImage(WithRole(r.Context(), role), image))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req(RoleAG, `C:\App\unravis.exe`, "/mcp/v1/call"); w.Code != 200 || w.Body.String() != "mcp" || reached != "/mcp/v1/call" {
		t.Errorf("the host's program: %d %q", w.Code, w.Body.String())
	}
	for _, image := range []string{`C:\App\unravis-desktop.exe`, `C:\App\other.exe`, ""} {
		if w := req(RoleAG, image, "/mcp/v1/call"); w.Code != 403 {
			t.Errorf("image %q: %d", image, w.Code)
		}
	}
	if w := req(RoleAG, `C:\App\unravis.exe`, "/mcp/v1/../api/cases"); w.Code != 404 {
		t.Errorf("an unclean path: %d", w.Code)
	}
	// the dashboard's own routes stay unreachable on ag
	if w := req(RoleAG, `C:\App\unravis.exe`, "/api/cases"); w.Code != 404 {
		t.Errorf("a gui route on ag: %d", w.Code)
	}
	if w := req(RoleUI, `C:\App\unravis.exe`, "/mcp/v1/call"); w.Body.String() == "mcp" {
		t.Errorf("the ui pipe reached the mcp handler")
	}
	plain := Handler(echoToken())
	r := httptest.NewRequest("POST", "/mcp/v1/call", nil)
	r = r.WithContext(WithPeerImage(WithRole(r.Context(), RoleAG), `C:\App\unravis.exe`))
	w := httptest.NewRecorder()
	plain.ServeHTTP(w, r)
	if w.Body.String() == "mcp" {
		t.Errorf("no option, but the mcp handler answered")
	}
}
