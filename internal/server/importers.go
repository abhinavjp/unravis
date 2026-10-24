package server

import (
	"net/http"
	"sort"

	"api-audit-explorer/internal/importer"
)

// GET /api/importers lists each importer and the owner-supplied settings it
// declares (card U3), so the import screen can offer exactly those keys. It
// carries the shape of each key (a fixed set of words, a bounded number, or
// bounded text), never a check function and never a stored value.

type suppliedSpecDTO struct {
	Key     string   `json:"key"`
	Kind    string   `json:"kind"`
	Values  []string `json:"values,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Integer bool     `json:"integer,omitempty"`
	MaxLen  int      `json:"maxLen,omitempty"`
}

type importerDTO struct {
	Name     string            `json:"name"`
	Supplied []suppliedSpecDTO `json:"supplied"`
}

func toSuppliedDTO(key string, s importer.SuppliedSpec) (suppliedSpecDTO, bool) {
	out := suppliedSpecDTO{Key: key}
	switch s.Kind {
	case importer.SuppliedEnum:
		out.Kind = "enum"
		out.Values = append([]string(nil), s.Values...)
	case importer.SuppliedNumber:
		out.Kind = "number"
		lo, hi := s.Min, s.Max
		out.Min, out.Max, out.Integer = &lo, &hi, s.Integer
	case importer.SuppliedText:
		out.Kind = "text"
		out.MaxLen = s.MaxLen
		if out.MaxLen <= 0 || out.MaxLen > importer.MaxSuppliedValue {
			out.MaxLen = importer.MaxSuppliedValue
		}
	default:
		return out, false
	}
	return out, true
}

func (h *handler) listImporters(w http.ResponseWriter, _ *http.Request) {
	if !h.casesAvailable(w) {
		return
	}
	out := []importerDTO{}
	if h.registry != nil {
		for _, name := range h.registry.Names() {
			imp, ok := h.registry.ByName(name)
			if !ok {
				continue
			}
			specs := importer.SuppliedKeysOf(imp)
			keys := make([]string, 0, len(specs))
			for k := range specs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			entry := importerDTO{Name: name, Supplied: []suppliedSpecDTO{}}
			for _, k := range keys {
				if dto, ok := toSuppliedDTO(k, specs[k]); ok {
					entry.Supplied = append(entry.Supplied, dto)
				}
			}
			out = append(out, entry)
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Importers []importerDTO `json:"importers"`
	}{out}, maxResponseBytes)
}
