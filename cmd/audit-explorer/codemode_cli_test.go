package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/server"
)

// BL-371 wiring: "unravis code types|run" reach the broker mountCodeMode builds, through the owner pipe's handler that main builds. The
// recorded run on a real sealed case store (surface "cli") is proven in internal/codeapi (TestCLIRunsThroughTheBroker); this proves main
// passes the broker in. With no broker the two commands answer ENDPOINT_UNAVAILABLE in plain words.
func TestCLICodeCommandsReachTheBrokerMainBuilds(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	cfg := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return cfg
		}
		return ""
	}
	mount := server.NewCodeMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithCodeMount(mount))
	broker := mountCodeMode(getenv, dash, token, mount)
	if broker == nil {
		t.Fatal("no broker")
	}
	f := runFlags{} // the pipe flags only parse on Windows; the handler needs none of them
	do := func(h http.Handler, op, args string) map[string]any {
		body := `{"op":"` + op + `","args":` + args + `,"requestId":"req_0123456789abcdef"}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(body)))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v: %s", op, err, w.Body)
		}
		return out
	}
	errOf := func(out map[string]any) (code, msg string) {
		e, _ := out["error"].(map[string]any)
		code, _ = e["code"].(string)
		msg, _ = e["message"].(string)
		return
	}

	h := cliHandlerFor(f, dash, token, nil, broker)
	// The declarations are the broker's, whether or not the switch is on.
	out := do(h, "code.types", `{}`)
	data, _ := out["data"].(map[string]any)
	if out["ok"] != true || data["dts"] == "" || data["schemaSha256"] == "" || data["namespace"] == "" {
		t.Fatalf("types: %v", out)
	}
	// Switched off (the file is not there): turned away in plain words, nothing recorded.
	out = do(h, "code.run", `{"code":"1"}`)
	if code, msg := errOf(out); out["ok"] != false || code != "ENDPOINT_UNAVAILABLE" || !strings.Contains(msg, "switched off") {
		t.Fatalf("run with the switch off: %v", out)
	}
	// Switched on: this build has no worker, so the broker's own refusal comes through (not "off", not "denied").
	if err := settingsfile.New(cfg).Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// A plain pipe client (the program on its own, nothing but the code) is still turned away: the command line may run scripts only
	// while the person in the app has allowed it for a while (BL-371). Switching code mode on does not do that.
	out = do(h, "code.run", `{"code":"1"}`)
	if code, msg := errOf(out); out["ok"] != false || code != "PERMISSION_DENIED" || !strings.Contains(msg, "not allowed to run scripts") {
		t.Fatalf("run with the command line shut: %v", out)
	}
	// The app's page opens the window (the session token is what only the app holds) and the broker's own answer comes through.
	put := httptest.NewRequest("PUT", "/api/code/settings", strings.NewReader(`{"cliMinutes":5}`))
	put.Host = "127.0.0.1:8765"
	put.Header.Set("X-Audit-Token", token)
	put.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	dash.ServeHTTP(w, put)
	if w.Code != 200 {
		t.Fatalf("opening the window: %d %s", w.Code, w.Body)
	}
	out = do(h, "code.run", `{"code":"1"}`)
	if code, msg := errOf(out); out["ok"] != false || code != "ENDPOINT_UNAVAILABLE" || !strings.Contains(msg, "safe runner") {
		t.Fatalf("run with no worker: %v", out)
	}

	// Without a broker the commands say so.
	h = cliHandlerFor(f, dash, token, nil, nil)
	for _, c := range []struct{ op, args string }{{"code.types", `{}`}, {"code.run", `{"code":"1"}`}} {
		out = do(h, c.op, c.args)
		if code, msg := errOf(out); out["ok"] != false || code != "ENDPOINT_UNAVAILABLE" || msg == "" {
			t.Errorf("%s without a broker: %v", c.op, out)
		}
	}
}

// main builds the code broker once and gives that same one to the owner pipe's command line handler. A nil passed there would leave
// "unravis code run" answering "no broker" in a build that has code mode, and the test above would stay green (it builds its own handler).
func TestMainGivesTheCodeBrokerToTheCLIHandler(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	if n := strings.Count(src, "cliHandlerFor("); n != 1 {
		t.Fatalf("want exactly one cliHandlerFor call in main.go, found %d", n)
	}
	if n := strings.Count(src, ":= mountCodeMode("); n != 1 {
		t.Fatalf("want exactly one mountCodeMode call in main.go, found %d", n)
	}
	at := strings.Index(src, "cliHandlerFor(")
	call := src[at:]
	if nl := strings.IndexByte(call, '\n'); nl >= 0 {
		call = call[:nl]
	}
	if !strings.Contains(call, "codeBroker))") {
		t.Fatalf("main.go does not pass the code broker to the CLI handler: %q", call)
	}
	if !strings.Contains(src, "codeBroker := mountCodeMode(") {
		t.Fatal("main.go does not name the mounted broker codeBroker")
	}
}

// "No time limit" on the real path: the app's settings route (the session token) turns it on, the owner pipe's "code run" then reaches the
// broker mountCodeMode built with no end time, a second mountCodeMode over the same settings folder (an app restart) finds the command
// line shut though code mode is still on in the file, and the file never holds the state.
func TestCLINoTimeLimitOnTheRealPathAndShutAfterARestart(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	cfg := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return cfg
		}
		return ""
	}
	if err := settingsfile.New(cfg).Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// start is one app start: a new mount, a new broker, a new gate.
	start := func() (dash http.Handler, h http.Handler) {
		mount := server.NewCodeMount()
		d := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithCodeMount(mount))
		broker := mountCodeMode(getenv, d, token, mount)
		if broker == nil {
			t.Fatal("no broker")
		}
		return d, cliHandlerFor(runFlags{}, d, token, nil, broker)
	}
	run := func(h http.Handler) (code string) {
		body := `{"op":"code.run","args":{"code":"1"},"requestId":"req_0123456789abcdef"}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(body)))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v: %s", err, w.Body)
		}
		e, _ := out["error"].(map[string]any)
		code, _ = e["code"].(string)
		return code
	}
	put := func(dash http.Handler, body string) (int, string) {
		req := httptest.NewRequest("PUT", "/api/code/settings", strings.NewReader(body))
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	get := func(dash http.Handler) map[string]any {
		req := httptest.NewRequest("GET", "/api/code/settings", nil)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", token)
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, req)
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
			t.Fatalf("settings = %d %s", w.Code, w.Body)
		}
		return m
	}

	dash, h := start()
	if run(h) != "PERMISSION_DENIED" || get(dash)["cliUnlimited"] != false {
		t.Fatal("a new app is not shut")
	}
	if code, body := put(dash, `{"cliUnlimited":true,"cliMinutes":5}`); code != 400 {
		t.Fatalf("both = %d %s", code, body)
	}
	if run(h) != "PERMISSION_DENIED" {
		t.Fatal("a refused request opened the command line")
	}
	if code, body := put(dash, `{"cliUnlimited":true}`); code != 200 {
		t.Fatalf("unlimited = %d %s", code, body)
	}
	if s := get(dash); s["cliUnlimited"] != true || s["cliOpenUntil"] != "" {
		t.Fatalf("settings: %v", s)
	}
	// Past the gate: the broker's own answer (this build has no worker), not "not allowed".
	if got := run(h); got != "ENDPOINT_UNAVAILABLE" {
		t.Fatalf("run while unlimited: %q", got)
	}
	b, err := os.ReadFile(settingsfile.New(cfg).Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(b)), "unlimited") {
		t.Fatalf("the state is in the file: %s", b)
	}

	// An app restart over the same folder: code mode is still on, the command line is shut.
	dash2, h2 := start()
	if s := get(dash2); s["enabled"] != true || s["cliUnlimited"] != false {
		t.Fatalf("after a restart: %v", s)
	}
	if got := run(h2); got != "PERMISSION_DENIED" {
		t.Fatalf("run after a restart: %q", got)
	}
	// And a timed window, then switching code mode off, ends it.
	if code, _ := put(dash2, `{"cliUnlimited":true}`); code != 200 {
		t.Fatal("reopen")
	}
	if code, _ := put(dash2, `{"enabled":false}`); code != 200 {
		t.Fatal("off")
	}
	if code, _ := put(dash2, `{"enabled":true}`); code != 200 {
		t.Fatal("on")
	}
	if got := run(h2); got != "PERMISSION_DENIED" {
		t.Fatalf("run after code mode went off and on: %q", got)
	}
}

// Code mode edited off in the file while the command line is open with no time limit: the next run attempt (which reads the settings
// first) shuts the command line, so editing the file back to on does not bring it back (Opus review of PR 339, F2 layer 1).
func TestCLIFileEditedOffShutsTheOpenCommandLineOnTheNextRun(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	cfg := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return cfg
		}
		return ""
	}
	files := settingsfile.New(cfg)
	if err := files.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mount := server.NewCodeMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithCodeMount(mount))
	broker := mountCodeMode(getenv, dash, token, mount)
	if broker == nil {
		t.Fatal("no broker")
	}
	h := cliHandlerFor(runFlags{}, dash, token, nil, broker)
	run := func() string {
		body := `{"op":"code.run","args":{"code":"1"},"requestId":"req_0123456789abcdef"}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(body)))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v: %s", err, w.Body)
		}
		e, _ := out["error"].(map[string]any)
		code, _ := e["code"].(string)
		return code
	}
	unlimited := func() any {
		req := httptest.NewRequest("GET", "/api/code/settings", nil)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", token)
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, req)
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
			t.Fatalf("settings = %d %s", w.Code, w.Body)
		}
		return m["cliUnlimited"]
	}
	req := httptest.NewRequest("PUT", "/api/code/settings", strings.NewReader(`{"cliUnlimited":true}`))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	dash.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("unlimited = %d %s", w.Code, w.Body)
	}
	if got := run(); got != "ENDPOINT_UNAVAILABLE" {
		t.Fatalf("run while unlimited: %q", got)
	}

	// The file is edited to "off" by hand; a run attempt reads the settings, and it shuts the command line on the way.
	if err := files.Save(codemode.Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	_ = run() // refused (the owner pipe gives the same answer for "off" as for "no worker in this build"); the point is what it did to the gate
	// Edited back to "on": the command line stays shut, in the run path and in what the settings say.
	if err := files.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "PERMISSION_DENIED" {
		t.Fatalf("run after the file went off and on: %q", got)
	}
	if unlimited() != false {
		t.Fatal("the settings still say no time limit")
	}
}

// BL-371: each change of the window is one app-log line. The settings hook that shuts the command line when a run finds code mode off logs
// it once (when it was open), not on every run and not when it was already shut.
func TestCLIHookLogsTheCloseOnceAndOnlyWhenTheWindowWasOpen(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	cfg := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return cfg
		}
		return ""
	}
	files := settingsfile.New(cfg)
	if err := files.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mount := server.NewCodeMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithCodeMount(mount))
	broker := mountCodeMode(getenv, dash, token, mount)
	if broker == nil {
		t.Fatal("no broker")
	}
	h := cliHandlerFor(runFlags{}, dash, token, nil, broker)
	run := func() {
		body := `{"op":"code.run","args":{"code":"1"},"requestId":"req_0123456789abcdef"}`
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(body)))
	}
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	const line = "Command line window: closed because code mode is off."

	// Code mode off and the window never opened: runs are refused, and nothing was closed, so nothing is logged.
	if err := files.Save(codemode.Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	run()
	if strings.Contains(buf.String(), line) {
		t.Fatalf("logged a close of a window that was never open: %q", buf.String())
	}
	// Open with no time limit (code mode back on), then off by a hand edit: the next run closes it and says so, once.
	if err := files.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/api/code/settings", strings.NewReader(`{"cliUnlimited":true}`))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	dash.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("unlimited = %d %s", w.Code, w.Body)
	}
	buf.Reset()
	if err := files.Save(codemode.Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	run()
	run()
	run()
	if n := strings.Count(buf.String(), line); n != 1 {
		t.Fatalf("%d close lines after three runs, want 1: %q", n, buf.String())
	}
}
