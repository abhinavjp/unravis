package server

import (
	"api-audit-explorer/internal/ledger"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// WithLedger attaches the hypothesis ledger. Without it the ledger routes answer 503.
func WithLedger(store ledger.Store) Option { return func(h *handler) { h.ledger = store } }

// serveLedger routes /api/ledger and /api/ledger/{id}[/reuse].
func (h *handler) serveLedger(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/ledger"), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			h.ledgerList(w)
		case http.MethodPost:
			h.ledgerCreate(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	idPart, action, hasAction := strings.Cut(rest, "/")
	if hasAction && action != "reuse" {
		writeError(w, http.StatusNotFound, "API route was not found.")
		return
	}
	id, err := url.PathUnescape(idPart)
	if err != nil || id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "Invalid hypothesis identifier.")
		return
	}
	switch {
	case hasAction && r.Method == http.MethodPost:
		h.ledgerReuse(w, id)
	case !hasAction && r.Method == http.MethodPut:
		h.ledgerUpdate(w, r, id)
	case !hasAction && r.Method == http.MethodDelete:
		h.ledgerDelete(w, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *handler) ledgerAvailable(w http.ResponseWriter) bool {
	if h.ledger == nil {
		writeError(w, http.StatusServiceUnavailable, "The ledger is unavailable.")
		return false
	}
	return true
}

func (h *handler) ledgerList(w http.ResponseWriter) {
	if !h.ledgerAvailable(w) {
		return
	}
	items, err := h.ledger.List()
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	if items == nil {
		items = []ledger.Hypothesis{}
	}
	writeJSON(w, http.StatusOK, items, maxResponseBytes)
}

func (h *handler) ledgerCreate(w http.ResponseWriter, r *http.Request) {
	if !h.ledgerAvailable(w) {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	item, err := h.ledger.Create(body.Text)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item, maxResponseBytes)
}

func (h *handler) ledgerUpdate(w http.ResponseWriter, r *http.Request, id string) {
	if !h.ledgerAvailable(w) {
		return
	}
	var input ledger.Input
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.ledger.Update(id, input)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item, maxResponseBytes)
}

func (h *handler) ledgerReuse(w http.ResponseWriter, id string) {
	if !h.ledgerAvailable(w) {
		return
	}
	item, err := h.ledger.Reuse(id)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item, maxResponseBytes)
}

func (h *handler) ledgerDelete(w http.ResponseWriter, id string) {
	if !h.ledgerAvailable(w) {
		return
	}
	if err := h.ledger.Delete(id); err != nil {
		writeLedgerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeLedgerError maps ledger errors to HTTP. A guard block carries its reasons.
func writeLedgerError(w http.ResponseWriter, err error) {
	var guard *ledger.GuardError
	switch {
	case errors.As(err, &guard) && guard != nil:
		reasons := guard.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		writeJSON(w, http.StatusUnprocessableEntity, struct {
			Error   string   `json:"error"`
			Code    string   `json:"code"`
			Reasons []string `json:"reasons"`
		}{Error: guard.Error(), Code: "guard_blocked", Reasons: reasons}, 64<<10)
	case errors.Is(err, ledger.ErrNotFound):
		writeError(w, http.StatusNotFound, "Hypothesis was not found.")
	case errors.Is(err, ledger.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "The ledger could not complete the request.")
	}
}
