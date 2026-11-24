package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"api-audit-explorer/internal/appdirs"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/version"
)

// openMCPEndpoint builds the MCP routes of the ag pipe (/mcp/v1/*, BL-344): the grants store (mcp.db, its server key sealed for the
// current Windows user), the dispatcher over the dashboard handler in this process, and the ledger. It returns a nil handler, and the
// routes stay a 404, whenever the store cannot be opened safely: there is no sealing off Windows, a newer store, a folder that cannot be
// restricted. A demo run that was given no folder of its own does not touch the person's real store. closeFn cancels the running calls
// and closes the store; call it after the servers have stopped.
func openMCPEndpoint(ctx context.Context, getenv func(string) string, dashboard http.Handler, token string, demo bool, prot func() (sealedfile.Protector, error)) (h http.Handler, closeFn func()) {
	noop := func() {}
	if demo && getenv(appdirs.EnvMCPDir) == "" && getenv("AUDIT_CONFIG_DIR") == "" {
		log.Print("MCP access is off in a demo run with no folder of its own (set AUDIT_CONFIG_DIR)")
		return nil, noop
	}
	p, err := prot()
	if err != nil {
		log.Print("MCP access is off: this PC cannot seal the grant key for the current user")
		return nil, noop
	}
	dir, err := appdirs.MCPDir(getenv)
	if err != nil {
		log.Print("MCP access is off: the MCP folder is not known")
		return nil, noop
	}
	db, recovered, err := mcpaudit.Open(ctx, dir, p, time.Now)
	if err != nil {
		// A fixed line: the error text of the store never names a path or a driver message.
		log.Printf("MCP access is off: %v", err)
		return nil, noop
	}
	if recovered != "" {
		log.Print("The MCP grant store was unreadable and was set aside; every host must be approved again")
	}
	if _, err := db.Prune(ctx, time.Now()); err != nil {
		log.Print("The MCP call ledger could not be pruned")
	}
	grants, err := mcpsrv.NewGrants(ctx, db, time.Now)
	if err != nil {
		_ = db.Close()
		log.Print("MCP access is off: the grant store could not be read")
		return nil, noop
	}
	gen := ""
	if start, serr := selfStart(); serr == nil && !start.IsZero() {
		gen = start.UTC().Format(time.RFC3339Nano)
	}
	h, err = mcpsrv.Handler(mcpsrv.Deps{
		Grants:   grants,
		Backend:  mcpsrv.NewLocalBackend(dashboard, token),
		Recorder: db,
		Info:     func() mcpsrv.Info { return mcpsrv.Info{PID: os.Getpid(), Gen: gen, AppVersion: version.Version} },
		Logf:     log.Printf,
	})
	if err != nil {
		_ = db.Close()
		log.Print("MCP access is off: the endpoint could not be built")
		return nil, noop
	}
	return h, func() { grants.CancelAll(); _ = db.Close() }
}
