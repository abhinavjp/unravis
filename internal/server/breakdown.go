package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"net/http"
)

func (h *handler) breakdown(w http.ResponseWriter, r *http.Request) {
	var input audit.BreakdownInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := audit.ValidateBreakdown(input); err != nil {
		writeRepoError(w, err)
		return
	}
	reader, ok := h.repo.(audit.BreakdownReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Breakdown is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	out, err := reader.Breakdown(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}
