package store

import (
	"time"

	"api-audit-explorer/internal/audit"
)

// partialRowCap is the code finishProvenance takes for a page cut at its row cap.
const partialRowCap = "rowcap"

// newProvenance starts a provenance entry for one source and window. mode is the
// mode actually run ("" is exact); a margin is carried only in hint mode. The scan
// class follows the window and whether the source has the index warning. A live
// entry never measures docsExamined, so it starts as ran_to_end.
func newProvenance(p *profile, window string, start, end time.Time, mode string, margin *audit.IDMargin) audit.Provenance {
	if mode != audit.QueryModeHint {
		mode, margin = audit.QueryModeExact, nil
	}
	pv := audit.Provenance{
		SourceID: p.source.ID, Window: window, Start: start.UTC(), End: end.UTC(),
		QueryMode: mode, ScanClass: audit.ScanClassOf(mode, end.Sub(start), p.indexWarning == ""),
		MaxTimeMs: audit.InteractiveQueryBudget.Milliseconds(), Margin: margin,
	}
	pv.Completeness = audit.CompletenessOf(pv)
	return pv
}

// finishProvenance records the outcome of a read. partialCode is empty for a read
// that ran to its end, "rowcap" for a page cut at its row cap, "query_timeout" for
// a timeout, and any other source error code for a read error. They become the
// partial reasons timeout, rowcap and error.
func finishProvenance(pv *audit.Provenance, returned, sample int64, elapsed time.Duration, partialCode string) {
	switch partialCode {
	case "":
		pv.PartialReason = ""
	case "query_timeout", "timeout":
		pv.PartialReason = "timeout"
	case partialRowCap:
		pv.PartialReason = partialRowCap
	default:
		pv.PartialReason = "error"
	}
	pv.DocsReturned, pv.SampleSize, pv.ElapsedMs = returned, sample, elapsed.Milliseconds()
	pv.Completeness = audit.CompletenessOf(*pv)
}

// notRunNote is the note on the entry of a source that never ran, because it was
// refused at admission. The entry stays exact, since that is the mode actually
// run (none), and the note records that hint mode was asked for but never tried.
// It is a reason alone, like every hint note. Nothing is noted for an exact query.
func notRunNote(requestedMode string) string {
	if requestedMode == audit.QueryModeHint {
		return "hint not attempted"
	}
	return ""
}

// failedProvenance is the entry for a source whose read did not finish.
func failedProvenance(p *profile, window string, start, end time.Time, mode string, margin *audit.IDMargin, note, code string, elapsed time.Duration) audit.Provenance {
	pv := newProvenance(p, window, start, end, mode, margin)
	pv.Note = note
	finishProvenance(&pv, 0, 0, elapsed, code)
	return pv
}
