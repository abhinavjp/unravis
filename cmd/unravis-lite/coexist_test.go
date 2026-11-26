package main

import (
	"encoding/json"
	"errors"
	"io"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/blackbox"
)

func TestInfoOverlayServesVersionAndPassesTheRestThrough(t *testing.T) {
	base := fstest.MapFS{
		"index.html":     {Data: []byte("<html>lite</html>")},
		"assets/lite.js": {Data: []byte("console.log(1)")},
	}
	ui := withInfo(base)

	raw, err := fs.ReadFile(ui, "lite-info.json")
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]string
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("not JSON: %v %s", err, raw)
	}
	for _, k := range []string{"app", "version", "full"} {
		if info[k] == "" {
			t.Errorf("key %q missing in %s", k, raw)
		}
	}
	if info["app"] != "Unravis Lite" || strings.HasPrefix(info["version"], "Unravis") || !strings.HasPrefix(info["full"], "Unravis Lite") {
		t.Errorf("unexpected content %s", raw)
	}
	st, err := fs.Stat(ui, "lite-info.json")
	if err != nil || st.Name() != "lite-info.json" || st.Size() != int64(len(raw)) || st.Mode() != 0o444 || st.IsDir() || !st.ModTime().IsZero() {
		t.Fatalf("stat: %+v %v", st, err)
	}

	if b, err := fs.ReadFile(ui, "index.html"); err != nil || string(b) != "<html>lite</html>" {
		t.Errorf("index.html: %q %v", b, err)
	}
	if b, err := fs.ReadFile(ui, "assets/lite.js"); err != nil || string(b) != "console.log(1)" {
		t.Errorf("assets/lite.js: %q %v", b, err)
	}
	if _, err := fs.ReadFile(ui, "nope.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := fs.ReadDir(ui, "."); err != nil {
		t.Errorf("listing the root: %v", err)
	}

	// Through the real handler: the allowlist lets a plain GET of a non-API path through.
	loc := blackbox.Location{Kind: blackbox.LocFolder, Path: t.TempDir()}
	const token = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(handler(loc, "Blackbox", token, "launch-code-launch-code", time.Now().Add(time.Minute), ui))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/lite-info.json", nil)
	req.Header.Set("X-Audit-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("served: %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if string(body) != string(raw) {
		t.Errorf("served body %s, want %s", body, raw)
	}
}

func TestTwoLitesAndBusyPortsDoNotClash(t *testing.T) {
	// Stand-ins for the full app and the Blackbox service.
	var busy []net.Listener
	for i := 0; i < 2; i++ {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		busy = append(busy, l)
	}
	ports := map[int]bool{}
	add := func(l net.Listener) {
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("not a TCP address: %v", l.Addr())
		}
		if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.IP.IsUnspecified() {
			t.Errorf("listening on %v, want 127.0.0.1 only", addr)
		}
		ports[addr.Port] = true
	}
	for _, l := range busy {
		add(l)
	}
	for i := 0; i < 2; i++ {
		l, err := listenLocal(0)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		add(l)
	}
	if len(ports) != 4 {
		t.Fatalf("want 4 different ports (2 busy, 2 Lite), got %v", ports)
	}

	taken := busy[0].Addr().(*net.TCPAddr).Port
	l, err := listenLocal(taken)
	if err == nil {
		l.Close()
		t.Fatal("a port that is taken must be refused")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Errorf("the error must name the port %d: %v", taken, err)
	}
}

func TestLiteWritesNothingAndTouchesNoSharedFolders(t *testing.T) {
	var watched []string
	fresh := func() string {
		d := t.TempDir()
		watched = append(watched, d)
		return d
	}
	tmp, local, roaming, profile, home, programData, cwd := fresh(), fresh(), fresh(), fresh(), fresh(), fresh(), fresh()
	for k, v := range map[string]string{
		"TMP": tmp, "TEMP": tmp, "TMPDIR": tmp, "LOCALAPPDATA": local, "APPDATA": roaming,
		"USERPROFILE": profile, "HOME": home, "ProgramData": programData,
	} {
		t.Setenv(k, v)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	// The recordings folder is outside the watched folders; Lite only reads it.
	rec := t.TempDir()
	if err := os.WriteFile(filepath.Join(rec, "bb-20260101-00.ndjson"), []byte("{\"t\":\"2026-01-01T00:00:00Z\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recBefore := snapshotDir(t, rec)
	loc := blackbox.Location{Kind: blackbox.LocFolder, Path: rec}
	const token = "0123456789abcdef0123456789abcdef"
	h := handler(loc, "Blackbox", token, "launch-code-launch-code", time.Now().Add(time.Minute), withInfo(fstest.MapFS{
		"index.html": {Data: []byte("<html>lite</html>")},
	}))
	for _, path := range []string{"/", "/lite-info.json", "/api/blackbox", "/api/sources", "/api/blackbox/bb-lite/series"} {
		method := "GET"
		if strings.HasSuffix(path, "/series") {
			method = "POST"
		}
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader("{}"))
		req.Header.Set("X-Audit-Token", token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req) // any status is fine here; it must not panic or write
		if (path == "/" || path == "/lite-info.json") && rr.Code != 200 {
			t.Errorf("%s answered %d, want 200", path, rr.Code)
		}
		if path == "/api/sources" && rr.Code != 404 {
			t.Errorf("denied route answered %d, want 404", rr.Code)
		}
	}

	if after := snapshotDir(t, rec); after != recBefore {
		t.Errorf("the recordings folder changed: before %s, after %s", recBefore, after)
	}
	for _, d := range watched {
		var found []string
		err := filepath.WalkDir(d, func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p != d {
				found = append(found, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) > 0 {
			t.Errorf("Lite wrote into %s: %v", d, found)
		}
	}
}

// snapshotDir lists every entry under dir with its size and modification time, so any write shows up as a difference.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s|%d|%d;", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
