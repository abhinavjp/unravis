package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"api-audit-explorer/internal/sqlconn"
)

// savedSQL keeps the SQL Server details typed in the create wizard. Resolve hands the stored password to the server-side health checks only;
// it is never sent to the browser.
type savedSQL interface {
	List() ([]sqlconn.Recent, error)
	Resolve(string) (sqlconn.Input, error)
	Save(sqlconn.Input) (sqlconn.Recent, error)
	Delete(string) error
}

const sqlUnavailable = "Saved SQL Server connections are unavailable on this platform."

func (h *handler) listSQLConnections(w http.ResponseWriter) {
	if h.sqlConns == nil {
		writeError(w, http.StatusServiceUnavailable, sqlUnavailable)
		return
	}
	items, err := h.sqlConns.List()
	if err != nil {
		if writeKeeperError(w, err) {
			return
		}
		writeError(w, http.StatusServiceUnavailable, "Saved SQL Server connections could not be read. The saved file was left unchanged.")
		return
	}
	if items == nil {
		items = []sqlconn.Recent{}
	}
	writeJSON(w, http.StatusOK, items, maxResponseBytes)
}

func (h *handler) saveSQLConnection(w http.ResponseWriter, r *http.Request) {
	if h.sqlConns == nil {
		writeError(w, http.StatusServiceUnavailable, sqlUnavailable)
		return
	}
	var input sqlconn.Input
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.sqlConns.Save(input)
	if err != nil {
		if errors.Is(err, sqlconn.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "SQL Server connection details are invalid. Check the server name, login and encryption choice.")
			return
		}
		if errors.Is(err, sqlconn.ErrFull) {
			writeError(w, http.StatusConflict, "You already have 20 saved SQL Server connections. Remove one first, then save this one. Nothing was removed for you.")
			return
		}
		if writeKeeperError(w, err) {
			return
		}
		writeError(w, http.StatusServiceUnavailable, "The SQL Server connection could not be saved. The existing file was left unchanged.")
		return
	}
	writeJSON(w, http.StatusOK, item, maxResponseBytes)
}

func (h *handler) deleteSQLConnection(w http.ResponseWriter, id string) {
	decoded, err := url.PathUnescape(id)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		writeError(w, http.StatusBadRequest, "Invalid saved connection identifier.")
		return
	}
	if h.sqlConns == nil {
		writeError(w, http.StatusServiceUnavailable, sqlUnavailable)
		return
	}
	if err := h.sqlConns.Delete(decoded); err != nil {
		if errors.Is(err, sqlconn.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Saved connection was not found.")
			return
		}
		if writeKeeperError(w, err) {
			return
		}
		writeError(w, http.StatusServiceUnavailable, "The saved connection could not be removed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
