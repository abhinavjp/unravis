package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/pipesrv"
	"api-audit-explorer/internal/server"
)

// call sends one request through the in-memory ui or ag pipe. Errors are returned, never raised through t (it runs off the test goroutine).
func (m *memListener) call(method, path, token string, body []byte) (int, string, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		cl, sv := net.Pipe()
		select {
		case m.ch <- sv:
			return cl, nil
		case <-m.done:
			return nil, net.ErrClosed
		}
	}}, Timeout: 20 * time.Second}
	req, err := http.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	if token != "" {
		req.Header.Set("X-Audit-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// TestShutdownRouteOnlyOnUIPipe: through run(), the route answers on the ui pipe only. The ag pipe and the loopback listener have no such
// route, so a request with the right token gets 404 there and nothing stops.
func TestShutdownRouteOnlyOnUIPipe(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	var agCode, tcpCode, uiGetCode int
	var callErr error
	var tcpL net.Listener
	var ui, ag *memListener
	ui, ag, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		var e1, e2, e3 error
		agCode, _, e1 = ag.call("POST", pipesrv.ShutdownPath, testToken, nil)
		uiGetCode, _, e2 = ui.call("GET", pipesrv.ShutdownPath, testToken, nil)
		req, _ := http.NewRequest("POST", "http://"+tcpL.Addr().String()+pipesrv.ShutdownPath, nil)
		req.Header.Set("X-Audit-Token", testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			e3 = err
		} else {
			tcpCode = resp.StatusCode
			_ = resp.Body.Close()
		}
		callErr = joinErrs(e1, e2, e3)
	})
	oldTCP := listenTCP
	listenTCP = func(port int) (net.Listener, error) {
		l, err := oldTCP(port)
		tcpL = l
		return l, err
	}
	t.Cleanup(func() { listenTCP = oldTCP })
	runUntilDone(t, []string{"--pipe-server", "--loopback", "--demo"}, getenv)
	if callErr != nil {
		t.Fatal(callErr)
	}
	if agCode != 404 {
		t.Errorf("ag pipe POST shutdown = %d, want 404", agCode)
	}
	if tcpCode != 404 {
		t.Errorf("loopback POST shutdown with the token = %d, want 404", tcpCode)
	}
	if uiGetCode != 405 {
		t.Errorf("ui pipe GET shutdown = %d, want 405", uiGetCode)
	}
}

func joinErrs(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// runUntilDone runs run() and fails when it does not return.
func runUntilDone(t *testing.T, args []string, getenv func(string) string) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run(args, getenv) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not stop")
	}
}

// TestShutdownRequiresToken: the real run() refuses the shutdown without the session token and stays up; the right token stops it.
func TestShutdownRequiresToken(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	var noTok, badTok, stillUp, good int
	var callErr error
	var ui *memListener
	ui, _, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel() // safety net: the test never hangs if the route fails to stop the app
		var e [4]error
		noTok, _, e[0] = ui.call("POST", pipesrv.ShutdownPath, "", nil)
		badTok, _, e[1] = ui.call("POST", pipesrv.ShutdownPath, "wrong-"+testToken, nil)
		stillUp, _, e[2] = ui.call("GET", "/api/info", testToken, nil)
		good, _, e[3] = ui.call("POST", pipesrv.ShutdownPath, testToken, nil)
		callErr = joinErrs(e[:]...)
	})
	runUntilDone(t, []string{"--pipe-only", "--demo"}, getenv)
	if callErr != nil {
		t.Fatal(callErr)
	}
	if noTok != 401 || badTok != 401 {
		t.Errorf("shutdown without/with wrong token = %d/%d, want 401/401", noTok, badTok)
	}
	if stillUp != 200 {
		t.Errorf("the app did not stay up after refused shutdowns: GET /api/info = %d", stillUp)
	}
	if good != 202 {
		t.Errorf("shutdown with the token = %d, want 202", good)
	}
}

// TestShutdownStopsRunWithoutAnySignal: the route alone ends run() with nil (exit code 0), inside the 5 s the shell waits.
func TestShutdownStopsRunWithoutAnySignal(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	var ui *memListener
	type result struct {
		code int
		err  error
	}
	got := make(chan result, 1)
	var calledAt atomic.Int64
	ui, _, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		go func() {
			time.Sleep(50 * time.Millisecond)
			calledAt.Store(time.Now().UnixNano())
			code, _, err := ui.call("POST", pipesrv.ShutdownPath, testToken, nil)
			got <- result{code, err}
		}()
	})
	runUntilDone(t, []string{"--pipe-only", "--demo"}, getenv)
	took := time.Since(time.Unix(0, calledAt.Load()))
	res := <-got
	if res.err != nil || res.code != 202 {
		t.Fatalf("shutdown call: %d %v", res.code, res.err)
	}
	if took > 5*time.Second {
		t.Fatalf("stop took %v after the call, want under the shell's 5 s", took)
	}
}

// walFiles lists every SQLite -wal, -shm and -journal file under root.
func walFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if !d.IsDir() && strings.HasSuffix(p, suffix) {
				out = append(out, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestShutdownCheckpointsWAL is spec 01 BL-303 on the real code path with app-created data: run() seeds real encrypted case databases
// (the demo's capture cases, written by the importer and store code), writers add checklist lines to one case through the ui pipe,
// the shutdown RPC is called while they are busy, and afterwards run() has returned nil, no -wal or -shm file is left anywhere under the
// data folder, the case database passes PRAGMA integrity_check, and every write the app acknowledged is in it.
func TestShutdownCheckpointsWAL(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken,
		envDemoCases: "1", envDemoNow: demoTestNow.Format(time.RFC3339),
	}
	getenv := envOf(env)
	caseID := string(demoCaseOpenID)

	var acked atomic.Int64
	var walSeenWhileBusy atomic.Bool
	var shutCode int
	var callErr error
	var mu sync.Mutex
	var ui *memListener
	ui, _, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		go func() {
			defer cancel() // safety net
			stop := make(chan struct{})
			var wg sync.WaitGroup
			for w := range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						body, _ := json.Marshal(map[string]any{"list": casestore.ListToDo, "text": "line " + strconv.Itoa(w) + " " + time.Now().Format(time.RFC3339Nano)})
						code, _, err := ui.call("POST", "/api/cases/"+caseID+"/checklist", testToken, body)
						if err == nil && code == http.StatusOK {
							acked.Add(1)
						}
					}
				}()
			}
			// Let the writers get going, then look for the WAL, then call shutdown with them still busy.
			deadline := time.Now().Add(20 * time.Second)
			for acked.Load() < 20 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if len(walFiles(t, dir)) > 0 {
				walSeenWhileBusy.Store(true)
			}
			var code int
			var err error
			code, _, err = ui.call("POST", pipesrv.ShutdownPath, testToken, nil)
			mu.Lock()
			shutCode, callErr = code, err
			mu.Unlock()
			close(stop)
			wg.Wait()
		}()
	})
	runUntilDone(t, []string{"--pipe-only", "--demo"}, getenv)
	time.Sleep(200 * time.Millisecond) // the caller goroutine finishes its bookkeeping
	mu.Lock()
	defer mu.Unlock()
	if callErr != nil || shutCode != 202 {
		t.Fatalf("shutdown call: %d %v", shutCode, callErr)
	}
	if acked.Load() < 20 {
		t.Fatalf("only %d writes were acknowledged; the load did not run (is the case writable?)", acked.Load())
	}
	t.Logf("%d acknowledged writes, -wal present while busy: %v", acked.Load(), walSeenWhileBusy.Load())
	if left := walFiles(t, dir); len(left) != 0 {
		t.Fatalf("files left after shutdown: %v", left)
	}

	verifyCaseAfterStop(t, getenv, caseID, acked.Load())
}

// verifyCaseAfterStop reopens the data folder the way the demo does and checks the case: PRAGMA integrity_check is ok and at least
// minLines of the to-do lines the writers were told were saved are in it.
func verifyCaseAfterStop(t *testing.T, getenv func(string) string, caseID string, minLines int64) {
	t.Helper()
	wiring, err := openCasesWith(getenv, newDemoLedger(t), caseSeams{perUser: true, seal: demoSeal, keySalt: func() []byte { return []byte(demoCaseKeySalt) }, now: func() time.Time { return demoTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	defer wiring.close()
	c, err := wiring.store.Open(context.Background(), casestore.CaseID(caseID))
	if err != nil {
		t.Fatalf("reopen the case: %v", err)
	}
	if chk, ok := c.(interface{ IntegrityCheck(context.Context) error }); !ok {
		t.Fatal("the case has no IntegrityCheck")
	} else if err := chk.IntegrityCheck(context.Background()); err != nil {
		t.Fatalf("integrity_check after shutdown: %v", err)
	}
	_ = c.Close()
	h := demoServer(t, append(wiring.options, server.WithCaptureCases()))
	req := httptest.NewRequest(http.MethodGet, "/api/cases/"+caseID+"/checklist", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", demoTestToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	var out struct {
		Items []struct {
			List string `json:"list"`
		} `json:"items"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("GET checklist = %d %.200s", res.Code, res.Body)
	}
	got := 0
	for _, it := range out.Items {
		if it.List == casestore.ListToDo {
			got++
		}
	}
	if int64(got) < minLines {
		t.Fatalf("%d to-do lines persisted, but %d writes were acknowledged", got, minLines)
	}
}
