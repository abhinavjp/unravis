package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
)

// The Erased lines (screen 124): after a case is erased the start screen keeps one
// line per case, "k7m2qx4a erased on 27 Oct 2026". A line is only an id and a time.
// It never holds a title or any case content, so the file is safe to keep when the
// case is gone. Clear removes the line and nothing else.

// erasedLinesMax bounds the lines kept; the oldest go first.
const erasedLinesMax = 100

// ErasedLine is one erased case.
type ErasedLine struct {
	ID casestore.CaseID `json:"id"`
	At time.Time        `json:"at"`
}

// ErasedLog keeps the Erased lines. Lines are oldest first.
type ErasedLog interface {
	Lines() []ErasedLine
	// Add records an erase. A second Add of one id replaces the first line.
	Add(id casestore.CaseID, at time.Time) error
	// Clear removes the line of one id. A missing line is not an error.
	Clear(id casestore.CaseID) error
}

type erasedFile struct {
	mu   sync.Mutex
	path string
}

// NewErasedFile keeps the lines in a small plain JSON file at path. An unreadable
// or damaged file reads as no lines: a lost line costs a sentence on the start
// screen and nothing else.
func NewErasedFile(path string) ErasedLog { return &erasedFile{path: path} }

type erasedDoc struct {
	Version int          `json:"version"`
	Lines   []ErasedLine `json:"lines"`
}

func (f *erasedFile) load() []ErasedLine {
	raw, err := os.ReadFile(f.path)
	if err != nil || len(raw) > 1<<20 {
		return nil
	}
	var doc erasedDoc
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	out := make([]ErasedLine, 0, len(doc.Lines))
	for _, l := range doc.Lines {
		if l.ID.Valid() && !l.At.IsZero() {
			out = append(out, l)
		}
	}
	return out
}

func (f *erasedFile) save(lines []ErasedLine) error {
	raw, err := json.Marshal(erasedDoc{Version: 1, Lines: lines})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (f *erasedFile) Lines() []ErasedLine {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}

func (f *erasedFile) Add(id casestore.CaseID, at time.Time) error {
	if !id.Valid() {
		return casestore.ErrBadID
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	lines := f.load()
	kept := lines[:0]
	for _, l := range lines {
		if l.ID != id {
			kept = append(kept, l)
		}
	}
	kept = append(kept, ErasedLine{ID: id, At: at.UTC()})
	if len(kept) > erasedLinesMax {
		kept = kept[len(kept)-erasedLinesMax:]
	}
	return f.save(kept)
}

func (f *erasedFile) Clear(id casestore.CaseID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	lines := f.load()
	kept := lines[:0]
	for _, l := range lines {
		if l.ID != id {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(lines) {
		return nil
	}
	return f.save(kept)
}

// WithErasedLog gives the server the Erased lines. Without it the list answers an
// empty array and an erase leaves no line.
func WithErasedLog(l ErasedLog) Option {
	return func(h *handler) { h.erased = l }
}

type erasedDTO struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

func (h *handler) erasedLines() []erasedDTO {
	out := []erasedDTO{}
	if h.erased == nil {
		return out
	}
	for _, l := range h.erased.Lines() {
		out = append(out, erasedDTO{ID: string(l.ID), At: l.At})
	}
	return out
}

// noteErased leaves the line for an erase. The case is already gone, so a failure
// is not worth failing the erase for.
func (h *handler) noteErased(id casestore.CaseID) {
	if h.erased != nil {
		_ = h.erased.Add(id, time.Now())
	}
}

// clearErased answers DELETE /api/cases/erased/{id}: only the line goes.
func (h *handler) clearErased(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	if h.erased != nil {
		if err := h.erased.Clear(id); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeCaseFailure(w, http.StatusInternalServerError, "store-error", "The case store could not complete the request.")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
