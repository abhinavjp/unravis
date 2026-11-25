package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
