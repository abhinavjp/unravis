package mcpsrv

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"api-audit-explorer/internal/mcpaudit"
)

// maxEnrollBody bounds the two routes below. A real body is under 1 KiB (an exe path is the longest field).
const maxEnrollBody = 8 << 10

var errBodyTooLarge = errors.New("mcpsrv: request body too large")

// claimReq is the body of POST /mcp/v1/enroll/claim.
type claimReq struct {
	Profile    string `json:"profile"`
	ClaimNonce string `json:"claimNonce"`
}

// EnrollRoutes registers the two routes of spec 03 section 3.3 that need no credential: enroll and enroll/claim. BL-344 owns the rest
// of the /mcp/v1/ mux (proof, call, cancel) and every other path stays a 404 there. Nothing is logged here: a request carries a nonce,
// and a claim answer carries the secret.
func (g *Grants) EnrollRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /mcp/v1/enroll", g.serveEnroll)
	mux.HandleFunc("POST /mcp/v1/enroll/claim", g.serveClaim)
}

func (g *Grants) serveEnroll(w http.ResponseWriter, r *http.Request) {
	var req EnrollReq
	if !readBody(w, r, &req) {
		return
	}
	resp, err := g.Enroll(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *Grants) serveClaim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	if !readBody(w, r, &req) {
		return
	}
	secret, rev, err := g.Claim(r.Context(), req.Profile, req.ClaimNonce)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Secret string `json:"secret"`
		Rev    int    `json:"profileRev"`
	}{secret, rev})
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnrollBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, errBodyTooLarge)
			return false
		}
		writeErr(w, ErrBadRequest)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // trailing data
		writeErr(w, ErrBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr answers with a fixed code and text per error: never the request's own text, a path or a driver message.
func writeErr(w http.ResponseWriter, err error) {
	status, code, msg := http.StatusInternalServerError, "internal", "The request could not be processed."
	switch {
	case errors.Is(err, ErrBadRequest):
		status, code, msg = http.StatusBadRequest, "invalid_arguments", "The request is not valid."
	case errors.Is(err, errBodyTooLarge):
		status, code, msg = http.StatusRequestEntityTooLarge, "invalid_arguments", "The request is too large."
	case errors.Is(err, ErrTooManyProfiles):
		status, code, msg = http.StatusTooManyRequests, "query_budget_exceeded", "Too many hosts are registered in Unravis."
	case errors.Is(err, ErrClaimPending):
		status, code, msg = http.StatusConflict, CodePermissionRequired, denied(ReasonPending).Message
	case errors.Is(err, ErrClaimGone):
		status, code, msg = http.StatusGone, CodePermissionRequired, "There is no credential to claim for this host."
	case errors.Is(err, ErrWrongNonce):
		status, code, msg = http.StatusForbidden, CodePermissionRequired, "This host cannot claim that credential."
	case errors.Is(err, mcpaudit.ErrNotFound):
		status, code, msg = http.StatusNotFound, CodePermissionRequired, "No such host."
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]any{"code": code, "message": msg}})
}
