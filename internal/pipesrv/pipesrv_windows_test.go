//go:build windows

package pipesrv

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/pipesec"
)

// TestServeOverRealPipes serves a handler over the real ui and ag pipes (pipesec.Listen) and calls both through pipesec.DialVerified.
// The test binary stands in for the Unravis image on both ends.
func TestServeOverRealPipes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pol := pipesec.PeerPolicy{InstallDir: filepath.Dir(exe), AllowedBase: []string{filepath.Base(exe)}, RequireSameLogon: true}
	p, err := Open(pipesec.Listen, pol)
	if err != nil {
		t.Fatal(err)
	}
	g := Serve(p, Handler(echoToken()), func(h http.Handler) *http.Server { return &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second} })
	t.Cleanup(func() { _ = g.Close() })

	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		t.Fatal(err)
	}
	sp := pipesec.ServerPolicy{ExpectedImage: exe, InstallDir: filepath.Dir(exe), ExpectedSession: session}
	get := func(name, path string) (int, string) {
		c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, _, err := pipesec.DialVerified(ctx, name, sp)
			return conn, err
		}}}
		resp, err := c.Get("http://127.0.0.1" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, b := get(p.UIName, "/api/cases"); c != 200 || b != "token=" {
		t.Fatalf("ui: %d %q", c, b)
	}
	if c, _ := get(p.AGName, "/api/cases"); c != 404 {
		t.Fatalf("ag GUI route: %d", c)
	}
	if c, b := get(p.AGName, "/mcp/v1/x"); c != 200 || b != "token=" {
		t.Fatalf("ag mcp route: %d %q", c, b)
	}
	// A second Open of the same names is impossible (random names), but the first-instance rule is pipesec's own test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
