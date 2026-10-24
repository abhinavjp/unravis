package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"api-audit-explorer/internal/audit"
)

// coverageRefused is the cov.refused copy: the 409 body of a census a live
// source does not run by default.
const coverageRefused = "This check reads every record in the window, so it does not run on a live source by default. Run it on a restored copy, or start Unravis with --allow-broad-scans."

// coverage serves GET /api/sources/{id}/coverage?start=&end= (RFC3339).
func (h *handler) coverage(w http.ResponseWriter, r *http.Request, sourceID string) {
	query := r.URL.Query()
	start, errStart := time.Parse(time.RFC3339, query.Get("start"))
	end, errEnd := time.Parse(time.RFC3339, query.Get("end"))
	if errStart != nil || errEnd != nil {
		writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 timestamps.")
		return
	}
	input := audit.CoverageInput{SourceID: sourceID, Start: start.UTC(), End: end.UTC()}
	if err := audit.ValidateCoverageInput(input); err != nil {
		writeRepoError(w, err)
		return
	}
	prober, ok := h.repo.(audit.CoverageProber)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "Coverage is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	report, err := prober.Coverage(ctx, input)
	if err != nil {
		if errors.Is(err, audit.ErrBroadScanRefused) {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{coverageRefused, "broad_scan_refused"}, 4096)
			return
		}
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report, maxResponseBytes)
}
