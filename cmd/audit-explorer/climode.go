package main

import (
	"net/http"
	"os"
	"time"

	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/version"
)

// newCLIHandler is the owner side of the command line (/cli/v1/* on the ag pipe, spec 05). It calls the dashboard handler in this
// process with the session token the process already holds, through the same tool code the MCP bridge runs. The command line itself
// never sees that token: the pipe's peer policy is the gate (BL-318, option B). Identifiers are shown, as in the app for its operator.
func newCLIHandler(dashboard http.Handler, token string) http.Handler {
	gen := ""
	if start, err := selfStart(); err == nil && !start.IsZero() {
		gen = start.UTC().Format(time.RFC3339Nano) // the locator's startTime, so the command line can match the two
	}
	bridge := mcp.NewLocal(dashboard, token, mcp.WithIdentifiers(true))
	return clisrv.Handler(clisrv.Deps{
		Backend:  clisrv.MCPBackend(bridge),
		Internal: clisrv.NewInternalGET(dashboard, token),
		Info: func() clisrv.Info {
			return clisrv.Info{PID: os.Getpid(), Gen: gen, AppVersion: version.Version, Owner: "app"}
		},
	})
}
