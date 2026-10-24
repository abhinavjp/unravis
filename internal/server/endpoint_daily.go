package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
)

// endpointDailyRefused is the 409 body when the daily series of one endpoint is
// asked for over more days than a live source allows.
const endpointDailyRefused = "The daily view reads one day at a time, so on a live source it covers at most 14 days. Choose a shorter span, or run it on a restored copy."

// endpointDaily serves GET /api/endpoint-daily?op=Controller.Action&sources=a,b
// &start=&end=&tz=. start and end are RFC 3339; tz is an IANA zone name (UTC
// when empty). It returns one daily p95 and request count per whole local day.
func (h *handler) endpointDaily(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	start, errStart := time.Parse(time.RFC3339, query.Get("start"))
	end, errEnd := time.Parse(time.RFC3339, query.Get("end"))
	if errStart != nil || errEnd != nil {
		writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 timestamps.")
		return
	}
	input := audit.EndpointDailyInput{
		SourceIDs: strings.Split(query.Get("sources"), ","),
		Start:     start.UTC(),
		End:       end.UTC(),
		TimeZone:  query.Get("tz"),
		Operation: query.Get("op"),
	}
	if err := audit.ValidateEndpointDaily(input); err != nil {
		writeRepoError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	out, err := audit.RunEndpointDaily(ctx, h.repo, input)
	if err != nil {
		if errors.Is(err, audit.ErrBroadScanRefused) {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{endpointDailyRefused, "broad_scan_refused"}, 4096)
			return
		}
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}
