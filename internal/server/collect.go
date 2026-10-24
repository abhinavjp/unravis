package server

import (
	"net/http"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/collect"
)

// collectItemDTO is one suggested log. Every field is fixed wording or a name
// from the importer registry, plus the host label the owner picked at import.
type collectItemDTO struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Title    string `json:"title"`
	Why      string `json:"why"`
	How      string `json:"how"`
	Importer string `json:"importer"`
	Rank     int    `json:"rank"`
}

// caseCollect answers GET /api/cases/{id}/collect: the logs still worth
// collecting, judged from the files already imported. It reads only the import
// records (importer name and host label), never an event, and the answer holds
// no value from any file. The case is read under the read share, like the other
// case reads, so an erase waits for it.
func (h *handler) caseCollect(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	records, err := c.Imports(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var have []collect.SourceCoverage
	var hosts []string
	seen := map[string]bool{}
	for _, rec := range records {
		// A quarantined file stored no events, and a duplicate repeats an
		// earlier import, so neither says what the case holds.
		if rec.Status != casestore.StatusOK {
			continue
		}
		have = append(have, collect.SourceCoverage{Importer: rec.Importer, Host: rec.HostLabel})
		if rec.HostLabel != "" && !seen[rec.HostLabel] {
			seen[rec.HostLabel] = true
			hosts = append(hosts, rec.HostLabel)
		}
	}
	items := collect.Suggest(have, hosts)
	out := make([]collectItemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, collectItemDTO{ID: it.ID, Host: it.Host, Title: it.Title, Why: it.Why, How: it.How,
			Importer: it.Importer, Rank: it.Rank})
	}
	writeJSON(w, http.StatusOK, struct {
		Items []collectItemDTO `json:"items"`
	}{out}, maxResponseBytes)
}
