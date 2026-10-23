package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"net/http"
)

func (h *handler) activity(w http.ResponseWriter, r *http.Request) {
	var input audit.ActivityInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if _, err := audit.ActivityQuery(input); err != nil {
		writeRepoError(w, err)
		return
	}
	reader, ok := h.repo.(audit.ActivityReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Activity context is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	page, err := reader.Activity(ctx, input)
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
