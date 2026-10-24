package server

import (
	"api-audit-explorer/internal/audit"
	"context"
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
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report, maxResponseBytes)
}
