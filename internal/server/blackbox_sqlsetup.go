package server

import (
	"context"
	"net/http"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sqlaccess"
)

// keeperHandover is the keeper side of "turn SQL collection on with this saved login": the keeper keeps the login in memory under a
// one-time id, the elevated helper fetches it over the keeper's pipe, and RevokeHandover forgets what is left. The saved password never
// reaches this process and is written to no file.
type keeperHandover interface {
	HandoverSaved(ctx context.Context, ref keeper.SQLRef) (keeper.SQLHandover, error)
	RevokeHandover(ctx context.Context, id string) error
}

// bbSQLSetupReq is "turn SQL collection on". Mode "login" uses the SQL login of the connection given (typed, or a saved one); mode "windows"
// uses the SQL service's own Windows account and needs no connection. Enable starts the service: with Windows sign-in only after the owner
// ran the grant, so the first call leaves it stopped and prints the grant.
type bbSQLSetupReq struct {
	sqlRequest
	Mode   string `json:"mode"`
	Enable bool   `json:"enable"`
}

// bbSQLSetupAnswer is what a finished setup says. GrantScript is set only for Windows sign-in.
type bbSQLSetupAnswer struct {
	OK          bool   `json:"ok"`
	Installed   bool   `json:"installed"`
	LoginSaved  bool   `json:"loginSaved"`
	Started     bool   `json:"started"`
	GrantScript string `json:"grantScript,omitempty"`
}

// bbSQLNotReady is the answer when the access check says the login is not ready: nothing was changed, and the report (with the script
// for what is missing) is what the person needs.
type bbSQLNotReady struct {
	OK     bool             `json:"ok"`
	Reason string           `json:"reason"`
	Error  string           `json:"error"`
	Report sqlaccess.Report `json:"report"`
}

// bbSQLSetup turns SQL collection on, on this PC. With a SQL login it checks access first (the same check as "Check Blackbox access") and
// changes nothing unless the login is ready; then ONE permission prompt installs the SQL service if needed, saves the login sealed in the
// service's locked folder and starts the service.
func (h *handler) bbSQLSetup(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	var req bbSQLSetupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Mode != "login" && req.Mode != "windows" {
		writeError(w, http.StatusBadRequest, "Pick how the SQL service signs in: a SQL login or Windows sign-in.")
		return
	}
	var file blackbox.SQLLoginFile
	var handover keeperHandover // set when the login is a saved one the key keeper holds
	var handoverRef keeper.SQLRef
	if req.Mode == "login" {
		if !h.tests.allow() {
			writeError(w, http.StatusTooManyRequests, "Too many connection tests in the last minute. Wait a minute, then try again.")
			return
		}
		target, _, ok := h.resolveSQL(w, req.sqlRequest)
		if !ok {
			return
		}
		k, ref, ok := h.heldSQL(w, req.sqlRequest)
		if !ok {
			return
		}
		shape := target
		if k != nil && shape.Password == "" {
			shape.Password = "x" // the keeper holds the real one; only the rest of the shape is checked here
		}
		stored, err := sqlaccess.StoredLoginFor(shape)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		var rep sqlaccess.Report
		switch {
		case h.demo:
			rep = sqlaccess.Report{Reached: true, Ready: true, Verdict: sqlaccess.VerdictReady, Checks: []sqlaccess.Check{}}
		case k != nil:
			ka, isAccess := k.(keeperAccess)
			if !isAccess {
				writeError(w, http.StatusServiceUnavailable, "This build cannot check a saved login that the key keeper holds.")
				return
			}
			var err error
			if rep, err = ka.AccessSaved(ctx, *ref); writeKeeperError(w, err) {
				return
			}
		default:
			rep = accessSQLFn(ctx, target)
		}
		if !rep.Ready {
			if rep.Checks == nil {
				rep.Checks = []sqlaccess.Check{}
			}
			writeJSON(w, http.StatusConflict, bbSQLNotReady{Reason: "access", Error: "This login is not ready for the Blackbox, so nothing was changed.", Report: rep}, maxResponseBytes)
			return
		}
		if k != nil {
			// A saved login stays in the keeper. It is handed over after the PC is locked (below), so the keeper's few minutes start when
			// the helper does, and never goes through a file.
			var held bool
			if handover, held = k.(keeperHandover); !held {
				writeError(w, http.StatusServiceUnavailable, "This build cannot hand a saved login to the Blackbox.")
				return
			}
			handoverRef = *ref
		} else {
			f, err := blackbox.WriteSQLLoginFile("", stored)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "The login could not be prepared for the Blackbox.")
				return
			}
			file = f
			defer blackbox.RemoveSQLLoginFile(f) // a typed login never outlives this call
		}
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbSQLSetupTimeout)
	defer cancel()
	var out blackbox.SQLSetupOutcome
	var err error
	if handover != nil {
		var ho keeper.SQLHandover
		if ho, err = handover.HandoverSaved(ctx, handoverRef); writeKeeperError(w, err) {
			return
		}
		// Whatever the helper did not take is forgotten by the keeper when this ends, however it ends.
		defer func() {
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer rcancel()
			_ = handover.RevokeHandover(rctx, ho.ID)
		}()
		out, err = pc.SetupSQLHandover(ctx, ho.ID, ho.Pipe, req.Enable)
	} else {
		out, err = pc.SetupSQL(ctx, file.Path, file.SHA, req.Mode == "windows", req.Enable)
	}
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	h.event(r, "blackbox_sql_setup", "mode", req.Mode, "enable", req.Enable, "started", out.Started)
	writeJSON(w, http.StatusOK, bbSQLSetupAnswer{OK: true, Installed: out.Installed, LoginSaved: out.LoginSaved, Started: out.Started, GrantScript: out.GrantScript}, 16<<10)
}

// bbSQLClearLogin deletes the saved SQL login on this PC (one prompt) and stops the SQL service.
func (h *handler) bbSQLClearLogin(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	if err := pc.ClearSQLLogin(ctx); err != nil {
		bbPCFailure(w, err)
		return
	}
	h.event(r, "blackbox_sql_login_cleared")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true}, 4096)
}
