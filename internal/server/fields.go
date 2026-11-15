package server

import (
	"context"
	"net/http"
	"strings"

	"api-audit-explorer/internal/audit"
)

// sourceFields serves GET /api/sources/{id}/fields: a read-only diagnostic that
// lists the field paths and BSON types of the source's newest documents so a
// stored field name can be found without reading any value. It sits behind the
// same session token and loopback host check as every other API route and is
// not used by the interface.
func (h *handler) sourceFields(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "Invalid source identifier.")
		return
	}
	sampler, ok := h.repo.(audit.FieldPathSampler)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "The field list is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	paths, err := sampler.SampleFieldPaths(ctx, id)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	if paths.Paths == nil {
		paths.Paths = []audit.FieldPath{}
	}
	for i := range paths.Paths {
		if paths.Paths[i].Types == nil {
			paths.Paths[i].Types = []string{}
		}
	}
	writeJSON(w, http.StatusOK, paths, maxResponseBytes)
}
