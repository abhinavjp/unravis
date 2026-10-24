package server

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/ledger"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Seams for the evidence engine. Production wiring is the real package; tests
// swap in fakes.
var (
	buildExport     = evidence.Build
	newExportKey    = evidence.NewKey
	canonicalExport = evidence.Canonical
)

const (
	exportToolVersion  = "dev"
	exportMaxPreviews  = 20
	exportPreviewTTL   = 15 * time.Minute
	exportGoneMessage  = "That preview is no longer available. Run the preview again."
	exportDefaultLabel = "source"
)

type exportPreviewRequest struct {
	SourceIDs         []string  `json:"sourceIds"`
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	IncludePseudonyms bool      `json:"includePseudonyms"`
	IncludeLedger     bool      `json:"includeLedger"`
}

type exportConfirmRequest struct {
	PreviewID string `json:"previewId"`
}

type exportPreviewResponse struct {
	PreviewID string          `json:"previewId"`
	Export    evidence.Export `json:"export"`
}

type exportConfirmResponse struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	SHA256   string `json:"sha256"`
}

type storedExport struct {
	canonical []byte
	sha256    string
	createdAt time.Time
}

// exportStore holds previews in memory until they are confirmed or expire. The
// per-preview pseudonym key is not kept: only the canonical bytes are.
type exportStore struct {
	mu    sync.Mutex
	items map[string]storedExport
	now   func() time.Time
}

func newExportStore() *exportStore {
	return &exportStore{items: map[string]storedExport{}, now: time.Now}
}

func (h *handler) exports() *exportStore {
	h.exportOnce.Do(func() { h.exportStore = newExportStore() })
	return h.exportStore
}

// evictLocked drops expired previews, then the oldest until there is room for one more.
func (s *exportStore) evictLocked() {
	now := s.now()
	for id, it := range s.items {
		if now.Sub(it.createdAt) >= exportPreviewTTL {
			delete(s.items, id)
		}
	}
	for len(s.items) >= exportMaxPreviews {
		oldest, found := "", false
		var at time.Time
		for id, it := range s.items {
			if !found || it.createdAt.Before(at) {
				oldest, at, found = id, it.createdAt, true
			}
		}
		delete(s.items, oldest)
	}
}

func (s *exportStore) put(id string, it storedExport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked()
	it.createdAt = s.now()
	s.items[id] = it
}

// take returns and removes a live preview.
func (s *exportStore) take(id string) (storedExport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return storedExport{}, false
	}
	delete(s.items, id)
	if s.now().Sub(it.createdAt) >= exportPreviewTTL {
		return storedExport{}, false
	}
	return it, true
}

func (h *handler) exportPreview(w http.ResponseWriter, r *http.Request) {
	var body exportPreviewRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	input := audit.PatternInput{SourceIDs: body.SourceIDs, Start: body.Start, End: body.End}
	if err := audit.ValidatePattern(input); err != nil {
		writeRepoError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	report, err := audit.RunPatterns(ctx, h.repo, input)
	if err != nil {
		// Checked before the generic mapping: this error also wraps ErrInvalidInput.
		if errors.Is(err, audit.ErrPatternsUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "Export is not available for this source. It cannot list the slowest requests.")
			return
		}
		writeRepoError(w, err)
		return
	}
	report = exportableReport(report)

	var hypotheses []ledger.Hypothesis
	if body.IncludeLedger && h.ledger != nil {
		all, err := h.ledger.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "The hypothesis ledger could not be read.")
			return
		}
		hypotheses = relatedHypotheses(all, report)
	}

	key, err := newExportKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	now := time.Now().UTC()
	export, err := buildExport(evidence.Input{
		Report:       report,
		SourceLabels: h.exportSourceLabels(ctx, body.SourceIDs),
		Ledger:       hypotheses,
		Options:      evidence.Options{IncludePseudonyms: body.IncludePseudonyms, IncludeLedger: body.IncludeLedger},
		ToolVersion:  exportToolVersion,
		Now:          now,
		Key:          key,
	})
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		writeExportBuildError(w, err)
		return
	}
	canonical, err := canonicalExport(export)
	if err != nil {
		writeExportBuildError(w, err)
		return
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	id := hex.EncodeToString(raw)
	// The confirm response's sha256 is over the exact content string it returns,
	// so the browser can check what it is about to save. The manifest's
	// bodySha256 inside the file is a different hash (manifest and body with
	// that value blanked) and is left as the engine wrote it.
	sum := sha256.Sum256(canonical)
	h.exports().put(id, storedExport{canonical: canonical, sha256: hex.EncodeToString(sum[:])})
	writeJSON(w, http.StatusOK, exportPreviewResponse{PreviewID: id, Export: export}, maxResponseBytes)
}

// exportableReport removes what the evidence export has no record type for: the
// findings of the rules added after the export schema (B3a, A3, B12r, B6g) and
// the tables behind them. The export engine refuses a rule it does not know, so
// they are left out of the file and out of ledger matching, and the other
// findings export as before.
func exportableReport(report audit.PatternReport) audit.PatternReport {
	kept := make([]audit.Finding, 0, len(report.Findings))
	for _, f := range report.Findings {
		switch f.Rule {
		case "B3a", "A3", "B12r", "B6g":
			continue
		}
		kept = append(kept, f)
	}
	report.Findings = kept
	report.OnsetMinutes, report.Concentration, report.Repeats, report.IdleGaps = nil, nil, nil, nil
	return report
}

// relatedHypotheses keeps only hypotheses that reference at least one finding
// in this report, so "include my ledger" never carries unrelated cases.
func relatedHypotheses(all []ledger.Hypothesis, report audit.PatternReport) []ledger.Hypothesis {
	ids := make(map[string]bool, len(report.Findings))
	for _, f := range report.Findings {
		if f.EvidenceID != "" {
			ids[f.EvidenceID] = true
		}
	}
	var out []ledger.Hypothesis
	for _, hyp := range all {
		for _, id := range hyp.EvidenceIDs {
			if ids[id] {
				out = append(out, hyp)
				break
			}
		}
	}
	return out
}

func writeExportBuildError(w http.ResponseWriter, err error) {
	if errors.Is(err, evidence.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	writeError(w, http.StatusInternalServerError, "The export could not be built.")
}

// exportSourceLabels maps source ids to labels. Ids missing from the repository
// get a generic label so an id never reaches the export.
func (h *handler) exportSourceLabels(ctx context.Context, ids []string) map[string]string {
	labels := make(map[string]string, len(ids))
	known := map[string]string{}
	if sources, err := h.repo.Sources(ctx); err == nil {
		for _, s := range sources {
			if strings.TrimSpace(s.Label) != "" {
				known[s.ID] = s.Label
			}
		}
	}
	for _, id := range ids {
		if label, ok := known[id]; ok {
			labels[id] = label
		} else {
			labels[id] = exportDefaultLabel
		}
	}
	return labels
}

func (h *handler) exportConfirm(w http.ResponseWriter, r *http.Request) {
	var body exportConfirmRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	it, ok := h.exports().take(body.PreviewID)
	if !ok {
		writeError(w, http.StatusNotFound, exportGoneMessage)
		return
	}
	writeJSON(w, http.StatusOK, exportConfirmResponse{
		Filename: "unravis-evidence-" + time.Now().UTC().Format("20060102-150405") + ".json",
		Content:  string(it.canonical),
		SHA256:   it.sha256,
	}, maxResponseBytes)
}
