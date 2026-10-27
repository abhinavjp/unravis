package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"api-audit-explorer/internal/casestore"
)

// A case's saved views (Manual tools) and checklist (four lists). Both live in
// the case file, travel with it and hold only what the person typed: a view's
// settings are filters, never a connection, a password or an address.
//
//	GET    /api/cases/{id}/views            200 {views: [...]}, newest first
//	POST   /api/cases/{id}/views            200 the stored view (no id: new; an id: replaces)
//	DELETE /api/cases/{id}/views/{vid}      200 {deleted: true}
//	GET    /api/cases/{id}/checklist        200 {items: [...]} by list, order, creation
//	POST   /api/cases/{id}/checklist        200 the stored item (new; goes last in its list unless order is sent)
//	PUT    /api/cases/{id}/checklist/{iid}  200 the stored item (replaces it; this is also how it moves list)
//	DELETE /api/cases/{id}/checklist/{iid}  200 {deleted: true}
//
// Answers: 400 "plan-invalid" for a body that breaks a rule (the message never
// repeats the input); 404 "case-not-found", "view-not-found" or
// "item-not-found"; 409 "case-closed" for a write to a closed case (reads still
// work); 413 "plan-full" at 200 views or 500 items, 413 "plan-too-large" for
// settings over 8 KiB; 423 while the master key is locked; 503 in the demo
// (except in the capture-only demo cases) and for a store that cannot keep them.

const (
	planDemoText     = "Saved views and the checklist are kept in a case, and cases are off in the demo."
	planInvalidText  = "That saved view or checklist item is not valid. Check the name, the list and the length."
	planSettingsText = "Saved view settings must be one object that holds no credentials or addresses."
	planClosedText   = "This case is closed. Its saved views and checklist can still be read but not changed."
	planViewFullText = "This case already holds as many saved views as it can keep. Delete one to save another."
	planItemFullText = "This case already holds as many checklist items as it can keep. Delete one to add another."
	planTooBigText   = "The saved view is larger than a case can keep."
)

/* ---------------------------------------------------------------- routing */

// casePlanItemPath returns a matcher for /api/cases/{id}/<seg>/{item}, handing
// the handlers "id/item".
func casePlanItemPath(seg string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		rest, ok := strings.CutPrefix(path, "/api/cases/")
		if !ok {
			return "", false
		}
		parts := strings.Split(rest, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] != seg || parts[2] == "" {
			return "", false
		}
		return parts[0] + "/" + parts[2], true
	}
}

/* ---------------------------------------------------------------- plumbing */

// casePlanOf opens the case for a views or checklist request (the same rule as
// caseLedgerOf: the capture-only demo cases work, a plain demo answers 503).
func (h *handler) casePlanOf(w http.ResponseWriter, r *http.Request, raw string) (casestore.Case, casestore.PlanStore, bool) {
	if h.demo && !h.captureCases {
		writeError(w, http.StatusServiceUnavailable, planDemoText)
		return nil, nil, false
	}
	if _, err := casestore.ParseID(raw); err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return nil, nil, false
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return nil, nil, false
	}
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	ps, ok := inner.(casestore.PlanStore)
	if !ok {
		c.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "plan-unavailable", "This case store cannot keep saved views or a checklist.")
		return nil, nil, false
	}
	return c, ps, true
}

// writePlanError maps a store error. what is "view" or "item": a missing id in a
// write or delete means the view or item, not the case, which was just opened.
func writePlanError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", planClosedText)
	case errors.Is(err, casestore.ErrBadPlan):
		writeCaseFailure(w, http.StatusBadRequest, "plan-invalid", planInvalidText)
	case errors.Is(err, casestore.ErrCapExceeded):
		text := planViewFullText
		if what == "item" {
			text = planItemFullText
		}
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "plan-full", text)
	case errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, what+"-not-found", "That "+what+" was not found.")
	case errors.Is(err, casestore.ErrHandleClosed):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	default:
		writeStoreError(w, err)
	}
}

// writePlanReadError is for a read, where ErrNotFound means the case is gone.
func writePlanReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, casestore.ErrNotFound) || errors.Is(err, casestore.ErrHandleClosed) {
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		return
	}
	writeStoreError(w, err)
}

/* ---------------------------------------------------------------- saved views */

type savedViewBody struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Tool     string          `json:"tool"`
	Settings json.RawMessage `json:"settings"`
}

func (h *handler) caseViewsList(w http.ResponseWriter, r *http.Request, raw string) {
	c, ps, ok := h.casePlanOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	views, err := ps.SavedViews(ctx)
	if err != nil {
		writePlanReadError(w, err)
		return
	}
	if views == nil {
		views = []casestore.SavedView{}
	}
	writeJSON(w, http.StatusOK, struct {
		Views []casestore.SavedView `json:"views"`
	}{views}, maxResponseBytes)
}

func (h *handler) caseViewSave(w http.ResponseWriter, r *http.Request, raw string) {
	c, ps, ok := h.casePlanOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body savedViewBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID != "" && !casestore.ValidPlanID(body.ID) {
		writeCaseFailure(w, http.StatusBadRequest, "plan-invalid", planInvalidText)
		return
	}
	v, err := casestore.CleanSavedView(casestore.SavedView{ID: body.ID, Name: body.Name, Tool: body.Tool, Settings: body.Settings})
	switch {
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "plan-too-large", planTooBigText)
		return
	case err != nil:
		writeCaseFailure(w, http.StatusBadRequest, "plan-invalid", planInvalidText)
		return
	}
	// The same rule as the workspace note: no key that names a credential or an
	// address, no pasted address, nothing nested past reason.
	if p := checkWorkspace(v.Settings); p != nil {
		writeCaseFailure(w, http.StatusBadRequest, "plan-invalid", planSettingsText)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	saved, err := ps.SaveView(ctx, v)
	if err != nil {
		writePlanError(w, err, "view")
		return
	}
	writeJSON(w, http.StatusOK, saved, maxResponseBytes)
}

func (h *handler) caseViewDelete(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, vid, _ := strings.Cut(arg, "/")
	c, ps, ok := h.casePlanOf(w, r, rawCase)
	if !ok {
		return
	}
	defer c.Close()
	if !casestore.ValidPlanID(vid) {
		writeCaseFailure(w, http.StatusNotFound, "view-not-found", "That view was not found.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := ps.DeleteView(ctx, vid); err != nil {
		writePlanError(w, err, "view")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Deleted bool `json:"deleted"`
	}{true}, 256)
}

/* ---------------------------------------------------------------- checklist */

type checklistBody struct {
	List    string `json:"list"`
	Text    string `json:"text"`
	Suspect string `json:"suspect"`
	Order   *int   `json:"order"`
}

func (h *handler) caseChecklistList(w http.ResponseWriter, r *http.Request, raw string) {
	c, ps, ok := h.casePlanOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	items, err := ps.ChecklistItems(ctx)
	if err != nil {
		writePlanReadError(w, err)
		return
	}
	if items == nil {
		items = []casestore.ChecklistItem{}
	}
	writeJSON(w, http.StatusOK, struct {
		Items []casestore.ChecklistItem `json:"items"`
	}{items}, maxResponseBytes)
}

func (h *handler) caseChecklistCreate(w http.ResponseWriter, r *http.Request, raw string) {
	c, ps, ok := h.casePlanOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body checklistBody
	if !decodeJSON(w, r, &body) {
		return
	}
	it := casestore.ChecklistItem{List: body.List, Text: body.Text, Suspect: body.Suspect}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if body.Order != nil {
		it.Order = *body.Order
	} else if items, err := ps.ChecklistItems(ctx); err == nil {
		// A new line goes last in its list.
		for _, o := range items {
			if o.List == it.List && o.Order >= it.Order {
				it.Order = o.Order + 1
			}
		}
	}
	h.putChecklist(w, ctx, ps, it)
}

func (h *handler) caseChecklistUpdate(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, iid, _ := strings.Cut(arg, "/")
	c, ps, ok := h.casePlanOf(w, r, rawCase)
	if !ok {
		return
	}
	defer c.Close()
	if !casestore.ValidPlanID(iid) {
		writeCaseFailure(w, http.StatusNotFound, "item-not-found", "That item was not found.")
		return
	}
	var body checklistBody
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	it := casestore.ChecklistItem{ID: iid, List: body.List, Text: body.Text, Suspect: body.Suspect}
	if body.Order != nil {
		it.Order = *body.Order
	} else if items, err := ps.ChecklistItems(ctx); err == nil {
		// Without an order, the line keeps its place when it stays in its list, and
		// goes last when it moves to another one.
		next, kept := 0, false
		for _, o := range items {
			if o.ID == iid && o.List == it.List {
				it.Order, kept = o.Order, true
			}
			if o.ID != iid && o.List == it.List && o.Order >= next {
				next = o.Order + 1
			}
		}
		if !kept {
			it.Order = next
		}
	}
	h.putChecklist(w, ctx, ps, it)
}

func (h *handler) putChecklist(w http.ResponseWriter, ctx context.Context, ps casestore.PlanStore, it casestore.ChecklistItem) {
	clean, err := casestore.CleanChecklistItem(it)
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "plan-invalid", planInvalidText)
		return
	}
	clean.ID = it.ID
	saved, err := ps.PutChecklistItem(ctx, clean)
	if err != nil {
		writePlanError(w, err, "item")
		return
	}
	writeJSON(w, http.StatusOK, saved, maxResponseBytes)
}

func (h *handler) caseChecklistDelete(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, iid, _ := strings.Cut(arg, "/")
	c, ps, ok := h.casePlanOf(w, r, rawCase)
	if !ok {
		return
	}
	defer c.Close()
	if !casestore.ValidPlanID(iid) {
		writeCaseFailure(w, http.StatusNotFound, "item-not-found", "That item was not found.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := ps.DeleteChecklistItem(ctx, iid); err != nil {
		writePlanError(w, err, "item")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Deleted bool `json:"deleted"`
	}{true}, 256)
}
