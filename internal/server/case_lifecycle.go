package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
)

// The case lifecycle routes (P2): reopen, the "needs another try" list, and the
// belief check every case move runs under the case's gate.
//
//	POST   /api/cases/{id}/reopen                   Closed to Open
//	PUT    /api/cases/{id}/retries                  replace the whole list
//	DELETE /api/cases/{id}/retries/{retryId}        drop one item
//	POST   /api/cases/{id}/retries/{retryId}/retry  run one item the server can run
//
// The list itself is read through GET /api/cases/{id} (the case view).
//
// A request that moves a case carries the state the page believes in the
// X-Expect-State header (open, closed, ...). Under the case's gate the server
// compares it with the state it finds (casestore.CheckBelief); a mismatch answers
// 409 "case-changed" with message M2. A request with no header skips the belief
// (an older page); the rows that do not depend on belief still run.

// expectStateHeader carries the state the page believes the case is in.
const expectStateHeader = "X-Expect-State"

const (
	caseChangedText       = "This case changed in another tab. Reload."
	archiveRunningText    = "An archive is already running."
	caseErasingText       = "This case is being erased."
	cancelArchiveText     = "An archive is running. Cancel it first."
	caseInUseText         = "The case is in use. Close its other tabs, then try again."
	caseAlreadyMadeText   = "This case was already made. Find it on the start screen."
	caseFreeingText       = "This case is being freed. Try again in a moment."
	badExpectStateText    = "The state the page expects is not one of the case states."
	badRequestIDText      = "The request id is not valid."
	badRetryText          = "A retry item is empty, too long, or holds characters or credentials that are not allowed."
	retryCapText          = "A case keeps at most 200 items that need another try."
	retryInPageText       = "This item is tried again from the page that holds its details."
	retryClosedText       = "The case is closed. Reopen it to try again."
	lifecycleDisabledText = "This case store cannot reopen cases."
)

// believedState reads the page's belief. ok false means the request was refused.
func believedState(w http.ResponseWriter, r *http.Request) (casestore.State, bool) {
	v := r.Header.Get(expectStateHeader)
	if v == "" {
		return "", true
	}
	st, ok := casestore.ParseState(v)
	if !ok {
		writeCaseFailure(w, http.StatusBadRequest, "bad-expect-state", badExpectStateText)
		return "", false
	}
	return st, true
}

// settle runs the recovery hook for a marked case (the per-call recovery of the
// marks framework) and returns the info with the mark as it is afterwards. An
// unmarked case costs nothing: no store call, no write.
func (h *handler) settle(ctx context.Context, info casestore.CaseInfo) casestore.CaseInfo {
	if info.Mark == "" || h.recoverer == nil {
		return info
	}
	mark, err := h.recoverer.RecoverIfMarked(ctx, info)
	if err == nil || mark != info.Mark {
		info.Mark = mark
	}
	return info
}

// markAnswer settles the case's mark and returns the refusal a mark that is still
// there earns (M19 for Freeing, M5 for Erasing), or nil.
func (h *handler) markAnswer(ctx context.Context, info casestore.CaseInfo) error {
	return casestore.CheckMark(h.settle(ctx, info).Mark)
}

// guard is the check every case move runs after it has taken the case's gate:
// recover a marked case, refuse one that must wait, then compare the belief with
// the state found. nil means the move may go ahead.
func (h *handler) guard(ctx context.Context, id casestore.CaseID, a casestore.Action, believed casestore.State) error {
	c, err := h.cases.Open(ctx, id)
	if err != nil {
		return err
	}
	info := c.Info()
	_ = c.Close()
	info = h.settle(ctx, info)
	if err := casestore.CheckMark(info.Mark); err != nil {
		return err
	}
	return casestore.CheckBelief(a, believed, casestore.StateOf(info))
}

// innerCase unwraps the read-share wrapper so an optional interface can be asserted.
func innerCase(c casestore.Case) casestore.Case {
	if sc, ok := c.(*sharedCase); ok {
		return sc.Case
	}
	return c
}

/* ------------------------------------------------------------------ DTOs */

type historyDTO struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
}

type retryDTO struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Reason  string          `json:"reason"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Created time.Time       `json:"created"`
}

func toRetryDTO(it casestore.RetryItem) retryDTO {
	return retryDTO{ID: it.ID, Kind: it.Kind, Key: it.Key, Label: it.Label, Reason: it.Reason, Payload: json.RawMessage(it.Payload), Created: it.Created.UTC()}
}

func toRetryDTOs(items []casestore.RetryItem) []retryDTO {
	out := make([]retryDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toRetryDTO(it))
	}
	return out
}

func toHistoryDTOs(h []casestore.HistoryEntry) []historyDTO {
	out := make([]historyDTO, 0, len(h))
	for _, e := range h {
		out = append(out, historyDTO{At: e.At.UTC(), Kind: e.Kind})
	}
	return out
}

// viewOf builds the case view of an opened case: the list fields, the counts, the
// history and the "needs another try" list. A case that cannot hold retries (or
// has none) shows an empty list.
func (h *handler) viewOf(ctx context.Context, c casestore.Case) caseViewDTO {
	info := c.Info()
	v := caseViewDTO{caseDTO: toCaseDTO(info), Envs: info.Envs, Sources: info.Sources, History: toHistoryDTOs(info.History), Retries: []retryDTO{}}
	if rs, ok := innerCase(c).(casestore.RetryStore); ok {
		if items, err := rs.Retries(ctx); err == nil {
			v.Retries = toRetryDTOs(items)
		}
	}
	return v
}

/* ---------------------------------------------------------------- reopen */

// reopenCase answers POST /api/cases/{id}/reopen. It clears the Closed stamp and
// writes a history entry. It does not start the sampler and does not touch the
// workspace note. The saved Mongo link and filters are not applied here: the page
// reads them back from the workspace note when it opens the case (a Closed case
// reads its note too), and its saving resumes once the case is Open again.
func (h *handler) reopenCase(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	lc, ok := h.cases.(casestore.Lifecycle)
	if !ok {
		writeCaseFailure(w, http.StatusServiceUnavailable, "lifecycle-unavailable", lifecycleDisabledText)
		return
	}
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	if err := h.guard(r.Context(), id, casestore.ActionReopen, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := lc.ReopenCase(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	c, err := h.cases.Open(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer c.Close()
	writeJSON(w, http.StatusOK, h.viewOf(r.Context(), c), maxResponseBytes)
}

/* --------------------------------------------------------------- retries */

// retryCaseOf takes the case's gate, opens the case for a retry request and returns its RetryStore and the state the page believes (""
// when the request carries none). The gate is the same slot Close, Reopen and import take, so a retry list change and a retry run cannot
// overlap a move of the case (A15), and the same item cannot run twice at once. done closes the case and lets go of the gate.
func (h *handler) retryCaseOf(w http.ResponseWriter, r *http.Request, raw string) (c casestore.Case, rs casestore.RetryStore, believed casestore.State, done func(), ok bool) {
	id, err := casestore.ParseID(raw)
	if err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return nil, nil, "", nil, false
	}
	if !h.casesAvailable(w) {
		return nil, nil, "", nil, false
	}
	believed, ok = believedState(w, r)
	if !ok {
		return nil, nil, "", nil, false
	}
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return nil, nil, "", nil, false
	}
	_, c, ok = h.openCaseForWrite(w, r, raw)
	if !ok {
		release()
		return nil, nil, "", nil, false
	}
	rs, ok = c.(casestore.RetryStore)
	if !ok {
		_ = c.Close()
		release()
		writeCaseFailure(w, http.StatusServiceUnavailable, "retries-unavailable", "This case store cannot keep a list of items to try again.")
		return nil, nil, "", nil, false
	}
	return c, rs, believed, func() { _ = c.Close(); release() }, true
}

func writeRetryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, casestore.ErrBadRetry):
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-retry", badRetryText)
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusUnprocessableEntity, "retry-cap-exceeded", retryCapText)
	case errors.Is(err, casestore.ErrHandleClosed), errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	default:
		writeStoreError(w, err)
	}
}

type retryItemBody struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Reason  string          `json:"reason"`
	Payload json.RawMessage `json:"payload"`
}

type retriesBody struct {
	Items []retryItemBody `json:"items"`
}

// credentialFree reports whether a retry item's text is free of anything that
// looks like a connection string or a password. The list holds labels only.
func credentialFree(parts ...string) bool {
	for _, p := range parts {
		if credentialLike.MatchString(p) || strings.Contains(p, "://") {
			return false
		}
	}
	return true
}

// putRetries replaces the case's list. The wizard sends the whole list of what
// failed after each run, so a repeated run never doubles an item.
func (h *handler) putRetries(w http.ResponseWriter, r *http.Request, raw string) {
	_, rs, _, done, ok := h.retryCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer done()
	var body retriesBody
	if !decodeJSON(w, r, &body) {
		return
	}
	items := make([]casestore.RetryItem, 0, len(body.Items))
	for _, it := range body.Items {
		if !credentialFree(it.Key, it.Label, string(it.Payload)) {
			writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-retry", badRetryText)
			return
		}
		items = append(items, casestore.RetryItem{Kind: it.Kind, Key: it.Key, Label: it.Label, Reason: it.Reason, Payload: []byte(it.Payload)})
	}
	saved, err := rs.ReplaceRetries(r.Context(), items)
	if err != nil {
		writeRetryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Retries []retryDTO `json:"retries"`
	}{toRetryDTOs(saved)}, maxResponseBytes)
}

// caseRetryPath matches /api/cases/{id}/retries/{retryId} and returns "id/retryId".
func caseRetryPath(path string) (string, bool) {
	parts, ok := retryParts(path)
	if !ok || len(parts) != 3 {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

// caseRetryRunPath matches /api/cases/{id}/retries/{retryId}/retry.
func caseRetryRunPath(path string) (string, bool) {
	parts, ok := retryParts(path)
	if !ok || len(parts) != 4 || parts[3] != "retry" {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

func retryParts(path string) ([]string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/cases/")
	if !ok {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "retries" || parts[2] == "" {
		return nil, false
	}
	return parts, true
}

func retryIDOf(w http.ResponseWriter, arg string) (rawCase string, id int64, ok bool) {
	rawCase, rawID, _ := strings.Cut(arg, "/")
	n, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || n <= 0 {
		writeCaseFailure(w, http.StatusNotFound, "retry-not-found", "That item is not in the list.")
		return "", 0, false
	}
	return rawCase, n, true
}

// deleteRetry drops one item. Dropping an item that is not there is not an error.
func (h *handler) deleteRetry(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, rid, ok := retryIDOf(w, arg)
	if !ok {
		return
	}
	_, rs, _, done, ok := h.retryCaseOf(w, r, rawCase)
	if !ok {
		return
	}
	defer done()
	if err := rs.RemoveRetry(r.Context(), rid); err != nil {
		writeRetryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runRetry tries one item again when the server holds everything it needs: the
// environments list and a plain source tag. The rest (a Mongo database, an audit
// source, an import) need the page's held connection or file, so the page tries
// them and then deletes the item. A Closed case refuses with case-closed.
func (h *handler) runRetry(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, rid, ok := retryIDOf(w, arg)
	if !ok {
		return
	}
	c, rs, believed, done, ok := h.retryCaseOf(w, r, rawCase)
	if !ok {
		return
	}
	defer done()
	// The page asked to run an item in the state it saw. If the case moved in another tab, that is M2, not a run in a state nobody chose.
	if believed != "" && believed != casestore.StateOf(c.Info()) {
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	items, err := rs.Retries(r.Context())
	if err != nil {
		writeRetryError(w, err)
		return
	}
	var item *casestore.RetryItem
	for i := range items {
		if items[i].ID == rid {
			item = &items[i]
			break
		}
	}
	if item == nil {
		writeCaseFailure(w, http.StatusNotFound, "retry-not-found", "That item is not in the list.")
		return
	}
	tg, ok := innerCase(c).(casestore.Tagger)
	if !ok {
		writeCaseFailure(w, http.StatusServiceUnavailable, "tags-unavailable", "This case store cannot hold environments or source tags.")
		return
	}
	if casestore.StateOf(c.Info()) != casestore.StateOpen {
		writeCaseFailure(w, http.StatusConflict, "case-closed", retryClosedText)
		return
	}
	// What the item asks for is read and checked first, so a bad or page-only item is refused with the list untouched.
	var (
		run     func() error
		capText string
	)
	switch item.Kind {
	case casestore.RetryEnvironments:
		var p environmentsDTO
		if err := json.Unmarshal(item.Payload, &p); err != nil {
			writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-retry", badRetryText)
			return
		}
		capText = tagEnvCapText
		run = func() error { return tg.SetEnvs(r.Context(), p.Environments) }
	case casestore.RetryTag:
		var p sourceTagBody
		if err := json.Unmarshal(item.Payload, &p); err != nil || p.Kind == mongoDatabaseKind || !credentialFree(p.Kind, p.Label, p.Env, p.Portal, p.API) {
			writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-retry", badRetryText)
			return
		}
		capText = tagSourceCapText
		run = func() error {
			_, err := tg.AddSource(r.Context(), casestore.SourceTag{ImportID: p.ImportID, Kind: p.Kind, Label: p.Label, Env: p.Env, Portal: p.Portal, API: p.API, Classification: p.Classification})
			return err
		}
	default:
		writeCaseFailure(w, http.StatusUnprocessableEntity, "retry-in-page", retryInPageText)
		return
	}
	// The item leaves the list before it runs, under the gate: a second press finds nothing to run, and a run that succeeds can never be
	// followed by a failed removal that would let the same tag go in twice. A run that fails puts the item back.
	if err := rs.RemoveRetry(r.Context(), rid); err != nil {
		writeRetryError(w, err)
		return
	}
	if err := run(); err != nil {
		_, _ = rs.NoteRetry(context.WithoutCancel(r.Context()), casestore.RetryItem{Kind: item.Kind, Key: item.Key, Label: item.Label, Reason: item.Reason, Payload: item.Payload})
		writeTagError(w, err, capText)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
