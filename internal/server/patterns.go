package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"errors"
	"net/http"
)

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
