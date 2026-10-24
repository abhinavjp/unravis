package evidence

import (
	"api-audit-explorer/internal/audit"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// genericSource is the label used when a report spans sources or no usable
	// human label exists.
	genericSource = "source"
	// notInExport replaces a ledger evidence reference to a record that is not
	// part of this export.
	notInExport  = "not in this export"
	tolerance    = "1 s _id resolution (ObjectId time is whole seconds)"
	timezoneRule = "UTC; request times are stored as UTC"
)

// reportWindow is the earliest provenance start and the latest provenance end,
// in UTC. A report without provenance cannot be placed in time.
func reportWindow(r audit.PatternReport) (time.Time, time.Time, error) {
	var start, end time.Time
	for _, p := range r.Provenance {
		if p.Start.IsZero() || p.End.IsZero() {
			continue
		}
		if start.IsZero() || p.Start.Before(start) {
			start = p.Start
		}
		if end.IsZero() || p.End.After(end) {
			end = p.End
		}
	}
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: the report has no provenance window", ErrInvalid)
	}
	return start.UTC(), end.UTC(), nil
}

// sourceLabel is the human label of the one source the report read. Pattern
// findings are per window, not per source, so a report over several sources
// (or one with no usable label) gets the generic "source". A label that looks
// like a connection string, URL, email, IP or ObjectId is not used either.
func sourceLabel(r audit.PatternReport, labels map[string]string) string {
	ids := map[string]bool{}
	for _, p := range r.Provenance {
		ids[p.SourceID] = true
	}
	for _, s := range r.Stalls {
		ids[s.SourceID] = true
	}
	for _, a := range r.AuditStalls {
		ids[a.SourceID] = true
	}
	if len(ids) != 1 {
		return genericSource
	}
	var id string
	for k := range ids {
		id = k
	}
	label := strings.TrimSpace(labels[id])
	if label == "" || label == id || scrubText(label) != label {
		return genericSource
	}
	return label
}

func validLabel(l string) bool {
	switch l {
	case audit.EvidenceSymptom, audit.EvidenceCorrelation, audit.EvidenceStrongCorrelation:
		return true
	}
	return false
}

func buildRecord(f audit.Finding, r audit.PatternReport, source string, start, end time.Time) (Record, error) {
	if !validLabel(f.Label) {
		return Record{}, fmt.Errorf("%w: finding %s has label %q", ErrInvalid, f.Rule, f.Label)
	}
	if f.EvidenceID == "" || scrubText(f.EvidenceID) != f.EvidenceID {
		return Record{}, fmt.Errorf("%w: finding %s has no usable evidence id", ErrInvalid, f.Rule)
	}
	headline, err := maskedHeadline(f, r)
	if err != nil {
		return Record{}, err
	}
	first, last := start, end
	n := MaskCount(f.N)
	rec := Record{
		Version:     SchemaVersion,
		EvidenceID:  f.EvidenceID,
		Source:      source,
		WindowStart: start,
		WindowEnd:   end,
		JoinClass:   "none",
		Tolerance:   tolerance,
		Provenance:  Provenance{Kind: "query", Ref: source},
		Label:       f.Label,
		Rule:        f.Rule,
		Headline:    headline,
		N:           n,
		// A masked count is low-n by definition. Copying the engine's flag
		// (n < 10) would split every "<20" cell into 1-9 and 10-19.
		LowN:      f.LowN || n.Masked,
		KnownGaps: knownGaps(f.Rule, r),
		Quality: SourceQuality{
			Threshold:     threshold(f.Rule, r.Params),
			CoverageFirst: &first,
			CoverageLast:  &last,
			TimezoneRule:  timezoneRule,
		},
	}
	h, err := recordHash(rec)
	if err != nil {
		return Record{}, err
	}
	rec.Provenance.Hash = h
	return rec, nil
}

// withheldText marks a count of ThinCell or more that is hidden so it cannot be
// subtracted from a printed total to recover a count below ThinCell.
const withheldText = "withheld"

// partOf names, for each rule whose n is a part of another rule's n, that
// total. B12 counts the requests inside volleys, a subset of the stalls B1
// counts (B2 prints the same total as B1).
var partOf = map[string]string{"B12": "B1"}

// suppressComplements applies complementary suppression across records: when a
// part and its total are both printed and they differ by 1 to ThinCell-1, the
// difference is a small cell anyone could compute, so the part is withheld. A
// masked part needs nothing: total minus "<20" gives only a range. It reports
// whether any count was withheld.
func suppressComplements(records []Record) (bool, error) {
	totals := map[string]int{}
	for _, r := range records {
		if r.N.Value != nil && !r.N.Masked {
			totals[r.Rule] = *r.N.Value
		}
	}
	withheld := false
	for i := range records {
		r := &records[i]
		total, ok := totals[partOf[r.Rule]]
		if !ok || r.N.Value == nil {
			continue
		}
		if d := total - *r.N.Value; d > 0 && d < ThinCell {
			r.N = Count{Masked: true, Text: withheldText}
			h, err := recordHash(*r)
			if err != nil {
				return false, err
			}
			r.Provenance.Hash = h
			withheld = true
		}
	}
	return withheld, nil
}

// recordHash is the SHA-256 hex of the record's compact JSON with
// Provenance.Hash empty, so it covers every other field including Kind and Ref.
func recordHash(rec Record) (string, error) {
	rec.Provenance.Hash = ""
	raw, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func threshold(rule string, p audit.PatternParams) string {
	if rule != "B1" {
		return ""
	}
	return fmt.Sprintf("stall at or above %s s", fmtSeconds(p.StallMs))
}

// knownGaps lists, in plain sentences, what the figures leave out. Every count
// in them is masked; the row cap is a limit, not a count, and stays exact.
func knownGaps(rule string, r audit.PatternReport) []string {
	gaps := []string{}
	if rule == "B6" {
		if r.AuditsTruncated {
			gaps = append(gaps, fmt.Sprintf("The audit-create read reached the cap of %d rows per source, so this count is a lower bound.", audit.MaxPatternRows))
		}
	} else if r.StallsTruncated {
		gaps = append(gaps, fmt.Sprintf("The stall read reached the cap of %d rows per source, so stall, episode and volley counts are lower bounds.", audit.MaxPatternRows))
	}
	q := r.Quality
	for _, c := range []struct {
		n    int64
		kind string
	}{{q.Incomplete, "incomplete"}, {q.Pending, "pending"}, {q.Invalid, "invalid"}} {
		if c.n > 0 {
			gaps = append(gaps, fmt.Sprintf("%s %s rows in the window are left out of these figures.", maskCount64(c.n).Text, c.kind))
		}
	}
	if r.Coverage == nil || !r.Coverage.Run {
		s := "Field coverage was not measured for this window."
		if r.Coverage != nil {
			if note := strings.TrimSpace(cleanFreeText(r.Coverage.Note, reportUserIDs(r), noteWithheld)); note != "" {
				s += " " + note
			}
		}
		gaps = append(gaps, s)
	}
	seen := map[string]bool{}
	for _, p := range r.Provenance {
		var s string
		switch {
		case p.Completeness == audit.Partial || p.PartialReason != "":
			s = fmt.Sprintf("A read was partial (%s), so its figures are lower bounds.", partialReason(p.PartialReason))
		case p.Completeness == audit.HintBounded || p.QueryMode == audit.QueryModeHint:
			s = "A read was bounded by a calibrated _id margin; rows outside the margin would be missed."
		}
		if s != "" && !seen[s] {
			seen[s] = true
			gaps = append(gaps, s)
		}
	}
	return gaps
}

// partialReason maps the provenance enum to words; anything else is not echoed.
func partialReason(r string) string {
	switch r {
	case "timeout":
		return "time limit reached"
	case "rowcap":
		return "row cap reached"
	case "error":
		return "read error"
	}
	return "reason not recorded"
}

// maskedHeadline rebuilds a finding's headline from the report's structured
// data so every count goes through MaskCount.
//
// DECISION: the engine's headline is generated text with counts in it, and the
// text alone cannot tell a count from a duration, so it is not sanitized but
// rebuilt. Durations and thresholds are not counts and stay exact. The §6.6
// wording "Audit create took <n> s as measured from the client side." is kept
// intact. Where the engine's wording varies with a small count (one episode or
// two, one volley or several, one audit stall or several), the export uses a
// single form for every case so the choice of form cannot reveal the count. An
// unknown rule is refused rather than passed through.
func maskedHeadline(f audit.Finding, r audit.PatternReport) (string, error) {
	p := r.Params
	stallSec := fmtSeconds(p.StallMs)
	n := MaskCount(f.N).Text
	switch f.Rule {
	case "B1":
		return fmt.Sprintf("%s requests took %s s or longer%s.", n, stallSec, partialNote(r.StallsTruncated)), nil
	case "B2":
		var h string
		if r.Recurrence == audit.RecurrenceRecurring {
			h = fmt.Sprintf("Recurring: %s separate episodes, %s requests in all.", MaskCount(len(r.Episodes)).Text, n)
		} else {
			h = fmt.Sprintf("Not recurring: fewer than %d separate episodes, %s requests in all.", audit.MinRecurringEpisodes, n)
		}
		if r.StallsTruncated {
			h += " The row limit was reached, so the episode count is partial."
		}
		return h, nil
	case "B6":
		if len(r.AuditStalls) == 0 {
			return "", fmt.Errorf("%w: B6 finding without audit-create rows", ErrInvalid)
		}
		longest := 0.0
		for _, a := range r.AuditStalls {
			longest = math.Max(longest, a.AuditCreateTimeMs)
		}
		share := strconv.FormatFloat(math.Round(p.AuditStallShare*1000)/10, 'f', -1, 64)
		h := fmt.Sprintf("Audit create took %s s as measured from the client side.", fmtSeconds(longest))
		h += fmt.Sprintf(" That is the longest of %s requests at or above %s s and %s%% of observed time.", n, fmtSeconds(p.AuditStallMs), share)
		if r.AuditsTruncated {
			h += " Partial count" + partialNote(true) + "."
		}
		return h, nil
	case "B12":
		return fmt.Sprintf("%s times, one user started 2 or more requests of %s s or longer within %s s of each other.",
			MaskCount(len(r.Volleys)).Text, stallSec, fmtSeconds(float64(p.VolleyWindowMs))), nil
	}
	return "", fmt.Errorf("%w: no export headline for rule %q", ErrInvalid, f.Rule)
}

func partialNote(truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf(" (at least; capped at %d rows per source)", audit.MaxPatternRows)
}

// fmtSeconds prints milliseconds as seconds with at most one decimal, as the engine does.
func fmtSeconds(ms float64) string {
	return strconv.FormatFloat(math.Round(ms/100)/10, 'f', -1, 64)
}
