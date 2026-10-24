package audit

import (
	"fmt"
	"time"
)

// lateWriteDelay is how long after the reference window ends the late row is
// first written.
const lateWriteDelay = 10 * time.Minute

// demoScope is the query mode one demo call runs in. In hint mode it carries
// the calibrated _id margin; when hint mode could not run, it carries the note
// that says why the call used exact mode instead.
type demoScope struct {
	mode   string
	margin *IDMargin
	note   string
}

// scopeFor resolves the mode a call runs in. Hint mode needs a calibrated
// margin; without one the call runs exact and its provenance says why.
func (d *demoRepository) scopeFor(requested string) demoScope {
	if requested != QueryModeHint {
		return demoScope{mode: QueryModeExact}
	}
	m, err := d.calibrate()
	if err != nil {
		return demoScope{mode: QueryModeExact, note: fmt.Sprintf("Hint mode was not available (%v); this result used exact mode.", err)}
	}
	return demoScope{mode: QueryModeHint, margin: &m}
}

// calibrate measures the demo's _id margin as §6.4 describes: the offset between
// each record's first-write time, truncated to a second, and its request time.
// The late-written rows are left out of the sample. They are the rows the margin
// is meant to miss, and a sample that held them would widen the margin to cover
// them.
func (d *demoRepository) calibrate() (IDMargin, error) {
	var offsets []time.Duration
	var from, to time.Time
	for _, r := range d.records {
		if d.lateWrites[r.ID] || r.InitiatedAt == nil || r.CreatedOn == nil {
			continue
		}
		offsets = append(offsets, r.CreatedOn.Truncate(time.Second).Sub(*r.InitiatedAt))
		if from.IsZero() || r.InitiatedAt.Before(from) {
			from = *r.InitiatedAt
		}
		if to.Before(*r.InitiatedAt) {
			to = *r.InitiatedAt
		}
	}
	observed, margin, err := MarginFromOffsets(offsets, IDMarginPad)
	if err != nil {
		return IDMargin{}, err
	}
	m := IDMargin{
		MarginMs:     margin.Milliseconds(),
		ObservedMs:   observed.Milliseconds(),
		PadMs:        IDMarginPad.Milliseconds(),
		Percentile:   99.9,
		SampleSize:   len(offsets),
		SampleFrom:   from,
		SampleTo:     to,
		CalibratedAt: d.now,
	}
	m.Label = HintLabel(m)
	return m, nil
}

// idAdmits reports whether a scan of [start, end) reaches the record. Hint mode
// bounds the _id index to the window widened by the margin on each side, and a
// record's _id time is its first-write time, truncated to a second. Exact mode
// reaches every record.
func (s demoScope) idAdmits(r RequestRecord, start, end time.Time) bool {
	if s.mode != QueryModeHint {
		return true
	}
	at := r.CreatedOn
	if at == nil {
		at = r.InitiatedAt
	}
	if at == nil {
		return false
	}
	id := at.Truncate(time.Second)
	pad := time.Duration(s.margin.MarginMs) * time.Millisecond
	return !id.Before(start.Add(-pad)) && !id.After(end.Add(pad))
}

// windowRecords returns the records a scan of [start, end) reads under the
// scope: all of them for exact mode, and only those inside the _id bound for
// hint mode.
func (s demoScope) windowRecords(records []RequestRecord, start, end time.Time) []RequestRecord {
	if s.mode != QueryModeHint {
		return records
	}
	out := make([]RequestRecord, 0, len(records))
	for _, r := range records {
		if s.idAdmits(r, start, end) {
			out = append(out, r)
		}
	}
	return out
}

// docsExamined counts the records of one source that a scan of [start, end)
// reads: every record of the source for exact mode, and the records inside the
// _id bound for hint mode.
func (d *demoRepository) docsExamined(sourceID string, scope demoScope, start, end time.Time) int64 {
	var n int64
	for _, r := range d.records {
		if r.SourceID == sourceID && scope.idAdmits(r, start, end) {
			n++
		}
	}
	return n
}

// demoProvenance describes one source's answer for one window. The demo is
// in memory and is treated as indexed, so its scans are narrow.
func (d *demoRepository) demoProvenance(sourceID, window string, start, end time.Time, scope demoScope, matched, returned int64, elapsed time.Duration) Provenance {
	examined := d.docsExamined(sourceID, scope, start, end)
	p := Provenance{
		SourceID:     sourceID,
		Window:       window,
		Start:        start,
		End:          end,
		QueryMode:    scope.mode,
		ScanClass:    ScanClassOf(scope.mode, end.Sub(start), true),
		DocsReturned: returned,
		DocsExamined: &examined,
		SampleSize:   matched,
		ElapsedMs:    elapsed.Milliseconds(),
		MaxTimeMs:    InteractiveQueryBudget.Milliseconds(),
		Note:         scope.note,
	}
	if scope.margin != nil {
		m := *scope.margin
		p.Margin = &m
	}
	p.Completeness = CompletenessOf(p)
	return p
}

// bucketCountsBySource totals the matched rows of each source's buckets.
func bucketCountsBySource(buckets []Bucket) map[string]int64 {
	counts := map[string]int64{}
	for _, b := range buckets {
		counts[b.SourceID] += b.Count
	}
	return counts
}

// newDemoWithLateWrite is the demo plus one late-written row per source: a
// Payroll.Run request initiated one minute into the reference window whose first
// write lands lateWriteDelay after that window ends. NewDemo is unchanged, so
// the --demo counts stay the same.
func newDemoWithLateWrite(now time.Time) *demoRepository {
	d := NewDemo(now).(*demoRepository)
	d.lateWrites = map[string]bool{}
	referenceStart := d.now.Add(-30 * time.Minute)
	referenceEnd := d.now.Add(-15 * time.Minute)
	for _, source := range d.sources {
		rec := demoRecord(source.ID, "Payroll", "Run", referenceStart.Add(time.Minute), 250, 0, 0, "reference")
		created := referenceEnd.Add(lateWriteDelay).Truncate(time.Second)
		updated := created.Add(time.Duration(*rec.PayloadRecordTimeMs * float64(time.Millisecond)))
		rec.ID = source.ID + "-Payroll-late-write"
		rec.CreatedOn, rec.UpdatedOn = &created, &updated
		d.records = append(d.records, rec)
		d.lateWrites[rec.ID] = true
	}
	return d
}
