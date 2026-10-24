package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"errors"
	"net/http"
)

// idleGapsRefused is the 409 body of the quiet-gap analysis (B6g) on a live source
// when the window is over 24 hours or the read would not be a narrow scan.
const idleGapsRefused = "Quiet-gap analysis reads every request in the window, so on a live source it runs for at most 24 hours at a time. Choose a shorter window, or run it on a restored copy."

// patterns runs the P2-lite incident rules. The engine reads through the
// existing repository budgets, so this handler adds no Mongo access of its own.
func (h *handler) patterns(w http.ResponseWriter, r *http.Request) {
	var input audit.PatternInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := audit.ValidatePattern(input); err != nil {
		writeRepoError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	report, err := audit.RunPatterns(ctx, h.repo, input)
	if err != nil {
		if errors.Is(err, audit.ErrBroadScanRefused) {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{idleGapsRefused, "broad_scan_refused"}, 4096)
			return
		}
		// Checked before the generic mapping: this error also wraps ErrInvalidInput.
		if errors.Is(err, audit.ErrPatternsUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "Patterns are not available for this source. It cannot list the slowest requests.")
			return
		}
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report, maxResponseBytes)
}
