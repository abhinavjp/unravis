package server

import (
	"context"
	"net/http"

	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sqlaccess"
)

// accessSQLFn runs the Blackbox access check on a login typed or saved without a stored password. A test replaces it.
var accessSQLFn = func(ctx context.Context, t dbhealth.SQLTarget) sqlaccess.Report {
	return sqlaccess.CheckTarget(ctx, t)
}

// keeperAccess is the keeper side of the check: a saved login whose password the keeper holds is checked there.
type keeperAccess interface {
	AccessSaved(ctx context.Context, ref keeper.SQLRef) (sqlaccess.Report, error)
}

// accessSQLConnection answers "can the Blackbox read this server with this login, and nothing more?". It connects once,
// changes nothing on the server and prints a script only when a right is missing. The password never leaves the keeper when
// the keeper holds it; a typed one is used for this call and dropped.
func (h *handler) accessSQLConnection(w http.ResponseWriter, r *http.Request) {
	if !h.tests.allow() {
		writeError(w, http.StatusTooManyRequests, "Too many connection tests in the last minute. Wait a minute, then test again.")
		return
	}
	var req sqlRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target, _, ok := h.resolveSQL(w, req)
	if !ok {
		return
	}
	k, ref, ok := h.heldSQL(w, req)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	var rep sqlaccess.Report
	switch {
	case h.demo:
		rep = demoAccessReport()
	case k != nil:
		ka, isAccess := k.(keeperAccess)
		if !isAccess {
			writeError(w, http.StatusServiceUnavailable, "This build cannot check a saved login that the key keeper holds.")
			return
		}
		var err error
		rep, err = ka.AccessSaved(ctx, *ref)
		if writeKeeperError(w, err) {
			return
		}
	default:
		rep = accessSQLFn(ctx, target)
	}
	if rep.Checks == nil {
		rep.Checks = []sqlaccess.Check{}
	}
	h.event(r, "sql_access_check", "verdict", rep.Verdict, "reached", rep.Reached)
	writeJSON(w, http.StatusOK, rep, maxResponseBytes)
}

// demoAccessReport is a made-up answer for the demo: a login that connects but is missing the server-state right, so the
// screen shows the script path. Nothing connects.
func demoAccessReport() sqlaccess.Report {
	return sqlaccess.Report{
		Reached: true, Version: "16.0.1000.6", Verdict: sqlaccess.VerdictNeedsGrant,
		Checks: []sqlaccess.Check{
			{ID: sqlaccess.CheckConnect, State: sqlaccess.Passed, Label: "Reach the server and sign in", Detail: "Connected (SQL Server 16.0.1000.6)."},
			{ID: sqlaccess.CheckRights, State: sqlaccess.Passed, Label: "Holds no more rights than needed", Detail: "Only connect and view server state. The Blackbox accepts this login."},
			{ID: sqlaccess.CheckRead, State: sqlaccess.Failed, Label: "Can read the activity views", Detail: "Refused: running requests, wait statistics. The login is missing the right to view server state."},
			{ID: sqlaccess.CheckWrite, State: sqlaccess.Passed, Label: "Cannot change data", Detail: "This login cannot insert, update, delete or alter in any database."},
		},
		Script: sqlaccess.GrantScript("unravis_ro", 16, true, false),
	}
}
