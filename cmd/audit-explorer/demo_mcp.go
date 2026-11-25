package main

import (
	"context"
	"log"
	"strings"
	"time"

	"api-audit-explorer/internal/appdirs"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcpadmin"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// envDemoMCP turns on the AI access screen in a --demo run with no pipes (the screenshot, audit and end-to-end runs, which are browser
// mode and so have no grant store of their own). The store is the real one (mcpaudit, mcpsrv.Grants, the same routes); only its
// sealing key is the fixed public demo one off Windows, as the demo cases use. Without --demo the variable is ignored.
const envDemoMCP = "UNRAVIS_DEMO_MCP"

// openDemoMCP opens the grant store in the demo's own folder (AUDIT_CONFIG_DIR/mcp, or UNRAVIS_MCP_DIR) and, when the store is empty,
// enrols five made-up hosts through the same Enroll, Approve, Claim and Revoke calls a real host and the screen make: two waiting,
// one waiting with a second program that tried to register as it, one approved, one revoked. It never runs without a folder of its
// own, so a demo run cannot touch the person's real store. The returned func closes the store.
func openDemoMCP(ctx context.Context, getenv func(string) string, repo audit.Repository, mount *server.MCPMount) func() {
	noop := func() {}
	if strings.TrimSpace(getenv(envDemoMCP)) != "1" {
		return noop
	}
	if getenv(appdirs.EnvMCPDir) == "" && getenv("AUDIT_CONFIG_DIR") == "" {
		log.Print(envDemoMCP + " needs AUDIT_CONFIG_DIR so the demo never touches your real MCP grants")
		return noop
	}
	prot, err := demoSeal()
	if err != nil {
		mount.SetOff(server.MCPOffSealing)
		return noop
	}
	dir, err := appdirs.MCPDir(getenv)
	if err != nil {
		mount.SetOff(server.MCPOffStore)
		return noop
	}
	db, _, err := mcpaudit.Open(ctx, dir, prot, time.Now)
	if err != nil {
		mount.SetOff(server.MCPOffStore)
		return noop
	}
	grants, err := mcpsrv.NewGrants(ctx, db, time.Now)
	if err != nil {
		_ = db.Close()
		mount.SetOff(server.MCPOffStore)
		return noop
	}
	if existing, lerr := grants.List(ctx); lerr == nil && len(existing) == 0 {
		var ids []string
		if sources, serr := repo.Sources(ctx); serr == nil {
			for _, s := range sources {
				ids = append(ids, s.ID)
			}
		}
		if err := seedDemoHosts(ctx, grants, ids); err != nil {
			log.Printf("The demo hosts could not be made: %v", err)
		}
	}
	admin, err := mcpadmin.Handler(mcpadmin.Deps{Grants: grants, Logf: log.Printf})
	if err != nil {
		_ = db.Close()
		mount.SetOff(server.MCPOffStore)
		return noop
	}
	mount.Set(admin)
	return func() { grants.CancelAll(); _ = db.Close() }
}

// seedDemoHosts makes the five demo hosts. Names and paths are made up; nothing here reaches a real host.
func seedDemoHosts(ctx context.Context, g *mcpsrv.Grants, sources []string) error {
	enroll := func(id, name, version, exe, nonce string, ident bool) (int, error) {
		r, err := g.Enroll(ctx, mcpsrv.EnrollReq{Profile: id, HostName: name, HostVersion: version, Exe: exe, PPID: 4100, ClaimNonce: nonce, RequestIdentifiers: ident})
		return r.Rev, err
	}
	if _, err := enroll("claude-desktop", "Claude Desktop", "1.4.2", `C:\Users\demo\AppData\Local\AnthropicClaude\app-1.4.2\claude.exe`, "AAAAAAAAAAAAAAAAAAAAAA", true); err != nil {
		return err
	}
	// The name carries tag characters on purpose: the screen must show them as text, never as markup.
	if _, err := enroll("cursor", "Cursor <b>Pro</b>", "0.45.1", `C:\Users\demo\AppData\Local\Programs\cursor\Cursor.exe`, "BBBBBBBBBBBBBBBBBBBBBB", false); err != nil {
		return err
	}
	// A second program registers as the same host with another nonce: the card must warn.
	if _, err := enroll("cursor", "Cursor", "0.45.1", `C:\Temp\cursor-helper.exe`, "CCCCCCCCCCCCCCCCCCCCCC", false); err != nil {
		return err
	}
	if _, err := enroll("zed", "Zed", "0.171.3", `C:\Users\demo\AppData\Local\Zed\zed.exe`, "FFFFFFFFFFFFFFFFFFFFFF", false); err != nil {
		return err
	}
	rev, err := enroll("vscode", "Visual Studio Code", "1.96.0", `C:\Users\demo\AppData\Local\Programs\Microsoft VS Code\Code.exe`, "DDDDDDDDDDDDDDDDDDDDDD", false)
	if err != nil {
		return err
	}
	a := mcpsrv.DefaultApproval(sources)
	if len(a.Sources) == 0 {
		return nil
	}
	a.ExpectRev = rev
	if _, err := g.Approve(ctx, "vscode", a); err != nil {
		return err
	}
	if _, _, err := g.Claim(ctx, "vscode", "DDDDDDDDDDDDDDDDDDDDDD"); err != nil {
		return err
	}
	rev, err = enroll("old-laptop", "Claude Desktop", "1.2.0", `D:\Tools\claude\claude.exe`, "EEEEEEEEEEEEEEEEEEEEEE", false)
	if err != nil {
		return err
	}
	a.ExpectRev = rev
	if _, err := g.Approve(ctx, "old-laptop", a); err != nil {
		return err
	}
	if _, _, err := g.Claim(ctx, "old-laptop", "EEEEEEEEEEEEEEEEEEEEEE"); err != nil {
		return err
	}
	_, err = g.Revoke(ctx, "old-laptop")
	return err
}
