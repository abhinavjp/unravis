package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"api-audit-explorer/internal/appdirs"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/mcpadmin"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/version"
)

// mcpOption adjusts openMCPEndpoint.
type mcpOption func(*mcpOpts)

type mcpOpts struct {
	mount *server.MCPMount
	code  *codemode.Broker
}

// withCodeBroker lets the MCP endpoint run unravis_types and unravis_execute for its grants through broker (BL-574 slice 2b). Without it
// (or with a nil broker) both tools answer that code mode is off.
func withCodeBroker(b *codemode.Broker) mcpOption { return func(o *mcpOpts) { o.code = b } }

// withMCPMount hands the AI access screen's routes (BL-347) to the dashboard server through m once the store is open; when the store
// stays closed, m learns why (a short code, never a path or an error text).
func withMCPMount(m *server.MCPMount) mcpOption { return func(o *mcpOpts) { o.mount = m } }

// openMCPEndpoint builds the MCP routes of the ag pipe (/mcp/v1/*, BL-344): the grants store (mcp.db, its server key sealed for the
// current Windows user), the dispatcher over the dashboard handler in this process, and the ledger. It returns a nil handler, and the
// routes stay a 404, whenever the store cannot be opened safely: there is no sealing off Windows, a newer store, a folder that cannot be
// restricted. A demo run that was given no folder of its own does not touch the person's real store. closeFn cancels the running calls
// and closes the store; call it after the servers have stopped.
func openMCPEndpoint(ctx context.Context, getenv func(string) string, dashboard http.Handler, token string, demo bool, prot func() (sealedfile.Protector, error), options ...mcpOption) (h http.Handler, closeFn func()) {
	var o mcpOpts
	for _, option := range options {
		option(&o)
	}
	off := func(reason string) {
		if o.mount != nil {
			o.mount.SetOff(reason)
		}
	}
	noop := func() {}
	if demo && getenv(appdirs.EnvMCPDir) == "" && getenv("AUDIT_CONFIG_DIR") == "" {
		log.Print("MCP access is off in a demo run with no folder of its own (set AUDIT_CONFIG_DIR)")
		off(server.MCPOffStore)
		return nil, noop
	}
	p, err := prot()
	if err != nil {
		log.Print("MCP access is off: this PC cannot seal the grant key for the current user")
		off(server.MCPOffSealing)
		return nil, noop
	}
	dir, err := appdirs.MCPDir(getenv)
	if err != nil {
		log.Print("MCP access is off: the MCP folder is not known")
		off(server.MCPOffStore)
		return nil, noop
	}
	db, recovered, err := mcpaudit.Open(ctx, dir, p, time.Now)
	if errors.Is(err, mcpaudit.ErrNewerVersion) {
		// BL-353: this build was rolled back to an older version. The store is left exactly as it is; the newer build reads it again.
		log.Print("MCP access is off: the approval store was written by a newer version of Unravis and is kept untouched until that version is back")
		off(server.MCPOffNewer)
		return nil, noop
	}
	if err != nil {
		// A fixed line: the error text of the store never names a path or a driver message.
		log.Printf("MCP access is off: %v", err)
		off(server.MCPOffStore)
		return nil, noop
	}
	if recovered != "" {
		log.Print("The MCP grant store was unreadable and was set aside; every host must be approved again")
	}
	if _, err := db.Prune(ctx, time.Now()); err != nil {
		log.Print("The MCP call ledger could not be pruned")
	}
	// A crash or a kill runs no shutdown hook, so every alias is cleared when the app starts: none outlives a restart. Not on the 24 h timer.
	if err := db.DropAllAliases(ctx); err != nil {
		log.Print("The MCP evidence references could not be cleared")
	}
	grants, err := mcpsrv.NewGrants(ctx, db, time.Now)
	if err != nil {
		_ = db.Close()
		log.Print("MCP access is off: the grant store could not be read")
		off(server.MCPOffStore)
		return nil, noop
	}
	gen := ""
	if start, serr := selfStart(); serr == nil && !start.IsZero() {
		gen = start.UTC().Format(time.RFC3339Nano)
	}
	// BL-349: evidence aliases (hooked to the grants: they go with a revoke, a reissue, the stop, the master-key lock and the shutdown), cursors signed with
	// the server key of the store, and the resolver the app's own route uses. The route is the dashboard's (token), not the ag pipe's.
	aliases := mcpsrv.NewAliases(db, grants, time.Now, log.Printf)
	backend := mcpsrv.NewLocalBackend(dashboard, token, mcpsrv.WithAliases(aliases), mcpsrv.WithCursorKey(db.MAC))
	server.SetEvidenceResolver(dashboard, func(c context.Context, alias string) (server.EvidenceTarget, bool) {
		e, err := aliases.ResolveOwner(c, alias)
		if err != nil {
			return server.EvidenceTarget{}, false
		}
		return server.EvidenceTarget{SourceID: e.SourceID, RecordID: e.RecordID, Host: e.ProfileID, ExpiresAt: e.ExpiresAt}, true
	})
	server.SetMasterKeyLockHook(dashboard, grants.CancelAll) // the master-key lock ends every host's handles and aliases
	disp, err := mcpsrv.NewDispatcher(mcpsrv.Deps{
		Grants:   grants,
		Backend:  backend,
		Recorder: db,
		Info:     func() mcpsrv.Info { return mcpsrv.Info{PID: os.Getpid(), Gen: gen, AppVersion: version.Version} },
		Code:     codeBackendFor(o.code),
		Logf:     log.Printf,
	})
	if err == nil {
		bindCodeBroker(o.code, disp, grants)
		h = disp.Handler()
	}
	if err != nil {
		server.SetEvidenceResolver(dashboard, nil)
		server.SetMasterKeyLockHook(dashboard, nil)
		_ = db.Close()
		log.Print("MCP access is off: the endpoint could not be built")
		off(server.MCPOffStore)
		return nil, noop
	}
	if o.mount != nil {
		admin, aerr := mcpadmin.Handler(mcpadmin.Deps{Grants: grants, Logf: log.Printf})
		if aerr != nil {
			grants.CancelAll()
			server.SetEvidenceResolver(dashboard, nil)
			server.SetMasterKeyLockHook(dashboard, nil)
			_ = db.Close()
			log.Print("MCP access is off: the approval screen could not be built")
			off(server.MCPOffStore)
			return nil, noop
		}
		o.mount.Set(admin)
	}
	// Idle work: expired comparison results leave memory, and the ledger and the aliases are pruned once a day.
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		sweep, prune := time.NewTicker(time.Minute), time.NewTicker(24*time.Hour)
		defer sweep.Stop()
		defer prune.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-sweep.C:
				backend.Sweep()
			case <-prune.C:
				if _, err := db.Prune(ctx, time.Now()); err != nil {
					log.Print("The MCP call ledger could not be pruned")
				}
			}
		}
	}()
	return h, func() {
		close(stop)
		<-stopped
		grants.CancelAll()
		server.SetEvidenceResolver(dashboard, nil)
		server.SetMasterKeyLockHook(dashboard, nil)
		_ = db.Close()
	}
}
