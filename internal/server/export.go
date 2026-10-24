package server

import "net/http"

// exportPreview and exportConfirm are filled in by the P13 server task.
func (h *handler) exportPreview(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Export is not available yet.")
}

func (h *handler) exportConfirm(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Export is not available yet.")
}
