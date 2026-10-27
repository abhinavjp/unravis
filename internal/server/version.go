package server

import (
	"net/http"

	"api-audit-explorer/internal/version"
)

// version answers GET /api/version with the build's stamped version. It sits behind the same session check as every other /api/ route.
func (h *handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}{version.Version, version.Commit, version.Date}, 4096)
}
