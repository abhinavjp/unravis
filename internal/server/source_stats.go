package server

import (
	"context"
	"net/http"

	"api-audit-explorer/internal/audit"
)

// sourceStats serves GET /api/sources/{id}/stats: the document count and newest
// time of one audit source, read from collection statistics and the _id index
// only. Docs is -1 when the count could not be read.
func (h *handler) sourceStats(w http.ResponseWriter, r *http.Request, sourceID string) {
	statter, ok := h.repo.(audit.SourceStatter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Source sizes are unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	stats, err := statter.Stats(ctx, audit.StatsInput{SourceID: sourceID})
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats, 4096)
}
