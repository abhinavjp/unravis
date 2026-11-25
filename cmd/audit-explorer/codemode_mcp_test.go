package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// codeRefusal reads the stable code of a refused code-mode answer ("" for anything else).
func codeRefusal(ans mcpsrv.Answer) string {
	var env struct {
		Error *struct{ Code string } `json:"error"`
	}
	if !ans.OK || !ans.IsError || json.Unmarshal(ans.Result, &env) != nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

// BL-574 slice 2b wiring: no broker means no backend (an untyped nil, so the dispatcher answers "code mode is off"), a broker gives the
// adapter with the case-lease functions of the HTTP routes, and binding a nil broker does nothing.
func TestCodeBackendForTheMCPEndpoint(t *testing.T) {
	if b := codeBackendFor(nil); b != nil {
		t.Fatalf("a backend without a broker: %#v", b)
	}
	bindCodeBroker(nil, nil, nil) // must not touch the nil dispatcher or grants

	broker, err := codemode.New(codemode.Config{Hosts: codemode.DashboardHosts(nil, "t"), Cases: func(context.Context, string) (c casestore.CodeRunStore, id string, err error) {
		return nil, "", codemode.ErrNoCase
	}})
	if err != nil {
		t.Fatal(err)
	}
	mc, ok := codeBackendFor(broker).(*codemode.MCPCode)
	if !ok || mc.Broker != broker || mc.Leases == nil || mc.ManyOpen == nil {
		t.Fatalf("backend %#v", codeBackendFor(broker))
	}
}

// The wiring main does, end to end on the demo repository: the broker mountCodeMode builds, the backend and the late binding. With the
// switch on, unravis_types answers the grant's declarations and unravis_execute reaches the broker and is refused because this build has no
// worker (not "code mode is off", not "denied"): that proves the dispatcher's admitter is bound. Revoking the grant refuses both tools.
func TestMCPCodeToolsReachTheBrokerMainBuilds(t *testing.T) {
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

	ctx := context.Background()
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), testSeal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	grants, err := mcpsrv.NewGrants(ctx, db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	disp, err := mcpsrv.NewDispatcher(mcpsrv.Deps{Grants: grants, Backend: mcpsrv.NewLocalBackend(dash, token), Recorder: db, Code: codeBackendFor(broker)})
	if err != nil {
		t.Fatal(err)
	}
	bindCodeBroker(broker, disp, grants)

	const nonce = "AAAAAAAAAAAAAAAAAAAAAA" // 16 bytes, base64url
	if _, err := grants.Enroll(ctx, mcpsrv.EnrollReq{Profile: "wired", HostName: "h", HostVersion: "1", Exe: `C:\h.exe`, PPID: 1, ClaimNonce: nonce}); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Approve(ctx, "wired", mcpsrv.Approval{Tier: mcpsrv.TierAggregate, Tools: []string{"audit_get_guide"}, Sources: []string{"demo-west"}, IdleTTL: mcpsrv.DefaultIdleTTL}); err != nil {
		t.Fatal(err)
	}
	secret, _, err := grants.Claim(ctx, "wired", nonce)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := grants.Authenticate(ctx, "Unravis-Grant wired."+secret)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	call := func(tool string) (int, mcpsrv.Answer) {
		n++
		args := map[string]any{}
		if tool == mcp.ToolExecute {
			args["code"] = "1"
		}
		body, _ := json.Marshal(map[string]any{"tool": tool, "args": args, "callId": []string{"00000001", "00000002", "00000003", "00000004", "00000005", "00000006"}[n-1]})
		return disp.CallAs(ctx, pr, body)
	}

	// Switched off (the file is not there): both tools say so.
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		if st, ans := call(tool); st != http.StatusOK || codeRefusal(ans) != "code_mode_off" {
			t.Fatalf("%s with the switch off: %d %+v", tool, st, ans)
		}
	}
	if err := settingsfile.New(cfg).Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st, ans := call(mcp.ToolTypes)
	var tb struct {
		DTS   string
		Tools []string
	}
	if st != http.StatusOK || !ans.OK || ans.IsError || json.Unmarshal(ans.Result, &tb) != nil || tb.DTS == "" || len(tb.Tools) != 1 || tb.Tools[0] != "audit_get_guide" {
		t.Fatalf("types: %d %+v", st, ans)
	}
	if st, ans := call(mcp.ToolExecute); st != http.StatusOK || codeRefusal(ans) != "sandbox_unavailable" {
		t.Fatalf("execute with no worker: %d %+v", st, ans)
	}
	if _, err := grants.Revoke(ctx, "wired"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		if st, ans := call(tool); st != http.StatusUnauthorized || ans.OK {
			t.Errorf("%s after the revoke: %d %+v", tool, st, ans)
		}
	}
}
