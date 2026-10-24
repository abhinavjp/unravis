package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"net/http"
	"strconv"
	"strings"
)

// schema serves GET /api/sources/{id}/schema?n=: a field-presence census of a
// sample of the source's audit documents. It returns names, types and counts
// only, never document values.
func (h *handler) schema(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "Invalid source identifier.")
		return
	}
	n := 0
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Sample size n must be a whole number.")
			return
		}
		n = parsed
	}
	sampler, ok := h.repo.(audit.SchemaSampler)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Schema census is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	census, err := sampler.SampleSchema(ctx, id, n)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	if census.Fields == nil {
		census.Fields = []audit.FieldPresence{}
	}
	for i := range census.Fields {
		if census.Fields[i].Types == nil {
			census.Fields[i].Types = []string{}
		}
	}
	writeJSON(w, http.StatusOK, census, maxResponseBytes)
}
