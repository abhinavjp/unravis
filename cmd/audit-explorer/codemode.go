package main

import (
	"log"
	"net/http"

	"api-audit-explorer/internal/codeapi"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/server"
)

// mountCodeMode turns on the code-mode routes (BL-574 slice 1). The routes are in the server's table of every run; they answer 503
// code_mode_unavailable until this has built the Broker over the server itself (a script reaches the read tools through the same
// dashboard handler the MCP server and the command line use) and hung the handler on the mount.
//
// What is real in this slice: the settings file, the routes, the case lease and the record of a run in the case. What is not there yet:
// the JavaScript worker. Spawner stays nil, so the Broker refuses every run with sandbox_unavailable before it records anything.
//
// Admit stays nil too. It charges the calls of a scoped run (a grant from the MCP surface) to the grant's budgets, and this slice has no
// scoped entrance: a run from these routes is the owner's own, unscoped, and the owner's runs are not charged. The Broker refuses a
// scoped run when Admit is nil, so a later slice cannot forget to set it. The dispatcher that supplies it is built inside the MCP endpoint,
// after this runs; that slice will pass it in.
func mountCodeMode(getenv func(string) string, served http.Handler, token string, mount *server.CodeMount) {
	dir, err := mcp.SessionDir(getenv)
	if err != nil {
		log.Print("Code mode is off: the settings folder is not known")
		return
	}
	// AllowIdentifiers (the operator's --mcp-allow-identifiers) is left false here on purpose: this slice has no entrance for it, so it
	// stays the safe default, and the settings screen shows false. The slice that adds the MCP entrance wires the flag through.
	files := settingsfile.New(dir)
	broker, err := codemode.New(codemode.Config{
		Hosts:    codemode.DashboardHosts(served, token),
		Cases:    codeapi.Resolver(mount),
		Settings: files.Settings,
	})
	if err != nil {
		// A fixed line: the error text is not needed to act on, and the routes stay at 503.
		log.Print("Code mode is off: the broker could not be built")
		return
	}
	codeapi.Install(codeapi.Options{Mount: mount, Broker: broker, Settings: files})
}
