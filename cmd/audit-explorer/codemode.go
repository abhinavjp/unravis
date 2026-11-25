package main

import (
	"log"
	"net/http"

	"api-audit-explorer/internal/codeapi"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// mountCodeMode turns on the code-mode routes (BL-574 slice 1). The routes are in the server's table of every run; they answer 503
// code_mode_unavailable until this has built the Broker over the server itself (a script reaches the read tools through the same
// dashboard handler the MCP server and the command line use) and hung the handler on the mount.
//
// What is real in this slice: the settings file, the routes, the case lease and the record of a run in the case. What is not there yet:
// the JavaScript worker. Spawner stays nil, so the Broker refuses every run with sandbox_unavailable before it records anything.
//
// Admit is not set here: it charges the calls of a scoped run (a grant from the MCP surface, slice 2b) to the grant's budgets, and the
// dispatcher that supplies it is built inside the MCP endpoint, after this runs. bindCodeBroker sets it there (SetAdmit). Until then, and
// whenever the MCP endpoint stays closed, the Broker refuses a scoped run, so a missing admitter fails closed. A run from these routes is the
// owner's own, unscoped, and the owner's runs are not charged.
func mountCodeMode(getenv func(string) string, served http.Handler, token string, mount *server.CodeMount) *codemode.Broker {
	dir, err := mcp.SessionDir(getenv)
	if err != nil {
		log.Print("Code mode is off: the settings folder is not known")
		return nil
	}
	// AllowIdentifiers (the operator's --mcp-allow-identifiers) is left false here on purpose, also now that the MCP entrance exists: it is
	// the safe default, the settings screen shows false, and no code run is ever given record ids. A later slice wires the flag through.
	files := settingsfile.New(dir)
	// The command-line window (BL-371): "unravis code run" is refused unless the person in the app has allowed it for a while. It is the
	// broker's Authorize hook, so it holds on every path to Run, and the settings route (cliMinutes) is the only thing that opens it.
	cliGate := codemode.NewCLIGate(nil)
	broker, err := codemode.New(codemode.Config{
		Hosts:     codemode.DashboardHosts(served, token),
		Cases:     codeapi.Resolver(mount),
		Settings:  files.Settings,
		Authorize: cliGate.Authorize,
	})
	if err != nil {
		// A fixed line: the error text is not needed to act on, and the routes stay at 503.
		log.Print("Code mode is off: the broker could not be built")
		return nil
	}
	codeapi.Install(codeapi.Options{Mount: mount, Broker: broker, Settings: files, CLIGate: cliGate})
	return broker
}

// codeBackendFor is the code-mode backend of the MCP endpoint (BL-574 slice 2b): the dispatcher runs unravis_types and unravis_execute
// through it for a host with a grant. A nil broker (code mode could not be mounted) gives a nil backend, and the dispatcher then refuses
// both tools as "code mode is off". The case resolver needs the lease scope of the HTTP routes, so the same two functions are passed in.
func codeBackendFor(broker *codemode.Broker) mcpsrv.CodeBackend {
	if broker == nil {
		return nil // an untyped nil, never a typed nil inside the interface
	}
	return &codemode.MCPCode{Broker: broker, Leases: codeapi.WithLeases, ManyOpen: codeapi.ManyOpen}
}

// bindCodeBroker connects the broker to the dispatcher and the grants it was built apart from: every call of a scoped run is charged to the
// grant's budgets (the dispatcher's AdmitCodeCall), and a revoke, a reissue or the stop ends the runs of that grant (CancelGrant). Both are
// needed before a scoped run is accepted: the broker refuses a scoped run that has no admitter.
func bindCodeBroker(broker *codemode.Broker, disp *mcpsrv.Dispatcher, grants *mcpsrv.Grants) {
	if broker == nil {
		return
	}
	broker.SetAdmit(disp.AdmitCodeCall)
	grants.OnRevoke(func(profileID string) { broker.CancelGrant(profileID) })
}
