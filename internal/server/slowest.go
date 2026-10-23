package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"net/http"
)

func (h *handler) slowest(w http.ResponseWriter, r *http.Request) {
	var input audit.PageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := audit.ValidateSlowest(input); err != nil {
		writeRepoError(w, err)
		return
	}
	reader, ok := h.repo.(audit.SlowestReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Slowest requests are unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	page, err := reader.Slowest(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	normalizePage(&page)
	for i := range page.Rows {
		page.Rows[i].URL = ""
	}
	writeJSON(w, http.StatusOK, page, maxResponseBytes)
}
