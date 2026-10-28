package investigate

import (
	"fmt"
	"math"
	"time"
)

// Step is one card of the guided analysis. Readiness is always one of the four
// fixed words: a step whose data is missing reads Cannot test yet, never a pass.
type Step struct {
	Number        int       `json:"number"`
	Name          string    `json:"name"`
	Readiness     Readiness `json:"readiness"`
	Reasons       []string  `json:"reasons"`
	UntestedSpans []Span    `json:"untestedSpans,omitempty"`
	Nutshell      string    `json:"nutshell"`
	Findings      []string  `json:"findings"`
}

func newStep(n int) Step {
	return Step{Number: n, Name: StepName(n), Nutshell: StepNutshell(n), Reasons: []string{}, Findings: []string{}}
}

func (s *Step) set(r Readiness, reason string) {
	s.Readiness = r
	if reason != "" {
		s.Reasons = append(s.Reasons, reason)
	}
}

// degrade lowers the readiness of a step to at most r and records why. Order of
// words, best to worst: Ready, Partly ready, Cannot test yet.
func (s *Step) degrade(r Readiness, reason string) {
	rank := map[Readiness]int{Ready: 0, PartlyReady: 1, CannotTestYet: 2, Failed: 3}
	if rank[r] > rank[s.Readiness] {
		s.Readiness = r
	}
	if reason != "" {
		s.Reasons = append(s.Reasons, reason)
	}
}

// fmtT writes a moment the way the rest of the app does ("28 Oct 2026 02:56 UTC"), so engine words and the report agree.
func fmtT(t time.Time) string { return t.UTC().Format("2 Jan 2006 15:04") + " UTC" }

func fmtMs(v *float64) string {
	if v == nil {
		return "not known"
	}
	return fmt.Sprintf("%.0f ms", *v)
}

// referenceAfter is true when the reference window sits after the incident
// window, so it describes the time after the slowdown rather than before it.
func referenceAfter(in Input) bool {
	return in.Incident.Valid() && in.Reference.Valid() && in.Reference.Start.After(in.Incident.Start)
}

// longGaps are the gap spans whose minutes cannot be tested (A35).
func longGaps(f Facts) []Span {
	var out []Span
	for _, g := range f.Gaps {
		if g.Kind == "long" {
			out = append(out, g)
		}
	}
	return out
}

func gapNote(f Facts) string {
	short, long := 0, 0
	for _, g := range f.Gaps {
		if g.Kind == "long" {
			long++
		} else {
			short++
		}
	}
	switch {
	case long > 0 && short > 0:
		return fmt.Sprintf("The data has %d long gap(s), which cannot be tested, and %d short gap(s), which are left out of the rates.", long, short)
	case long > 0:
		return fmt.Sprintf("The data has %d long gap(s); those minutes cannot be tested and are never counted as zero.", long)
	default:
		return fmt.Sprintf("The data has %d short gap(s); the minutes that exist are used and the missing ones are left out, never counted as zero.", short)
	}
}

func hasAudit(in Input, f Facts) bool { return in.Audit == AuditPresent && f.Counts.Used > 0 }

func noAuditReason(in Input) string {
	switch in.Audit {
	case AuditMissing:
		return "There is no audit source in this case."
	case AuditFailed:
		return "The audit source could not be read."
	default:
		return "No audited call falls inside either window."
	}
}

// laneGroup lists the catalog ids of one lane step.
func laneGroup(group string) []CatalogEntry {
	var out []CatalogEntry
	for _, e := range Catalog() {
		if e.Group == group {
			out = append(out, e)
		}
	}
	return out
}

// greyReason is the sentence for suspects held grey, in the right singular or plural.
func greyReason(grey int) string {
	if grey == 1 {
		return "1 suspect is held grey in this release and is not tested."
	}
	return fmt.Sprintf("%d suspects are held grey in this release and are not tested.", grey)
}

// laneCount says how many of the entries have a reading from their own lane.
// Entries held grey in Release 1 never count as read.
func laneCount(in Input, entries []CatalogEntry) (known, total, grey int) {
	for _, e := range entries {
		total++
		if e.GreyR1 {
			grey++
			continue
		}
		if v, ok := in.Lanes[e.ID]; ok && v != Unknown {
			known++
		}
	}
	return
}

// Steps builds the eleven step cards from the facts and signals. Only Step 10's
// findings are left to the origin evaluation.
func Steps(in Input, f Facts, s Signals, th Thresholds) []Step {
	steps := make([]Step, StepCount)
	for i := range steps {
		steps[i] = newStep(i)
	}
	audit := hasAudit(in, f)

	// Step 0: Frame.
	{
		st := &steps[0]
		switch {
		case !in.Incident.Valid() || !in.Reference.Valid():
			st.set(Failed, "Both windows need a start and a later end.")
		case in.Incident.Overlaps(in.Reference):
			st.set(Failed, "The incident window and the reference window overlap.")
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		default:
			st.set(Ready, "")
			if referenceAfter(in) {
				st.degrade(PartlyReady, "The reference window comes after the incident window, so it may already carry the same slowness.")
			}
			if f.Ref.N < th.GroupSizeP95 {
				st.degrade(PartlyReady, fmt.Sprintf("The reference window holds %d calls; at least %d are needed for a steady baseline.", f.Ref.N, th.GroupSizeP95))
			}
		}
		if in.Incident.Valid() {
			st.Findings = append(st.Findings, "Incident window: "+fmtT(in.Incident.Start)+" to "+fmtT(in.Incident.End)+".")
		}
		if in.Reference.Valid() {
			st.Findings = append(st.Findings, "Reference window: "+fmtT(in.Reference.Start)+" to "+fmtT(in.Reference.End)+".")
		}
		if audit {
			st.Findings = append(st.Findings, fmt.Sprintf("Calls used: %d in the incident window, %d in the reference window.", f.Counts.Incident, f.Counts.Reference))
		}
		if in.Route != "" {
			st.Findings = append(st.Findings, "Narrowed to "+in.Route+" for Steps 2 to 4.")
		}
	}

	// Step 1: Trust data. Never Ready in Phase A: a time-zone or clock-offset check
	// needs a second source with its own clock.
	{
		st := &steps[1]
		switch {
		case in.Audit == AuditFailed:
			st.set(Failed, "The audit source could not be read.")
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		default:
			st.set(PartlyReady, "Only the audit was checked. A time-zone or clock-offset check needs a second source with its own clock.")
			if f.Clock.Anomaly == Yes {
				st.Reasons = append(st.Reasons, "Some calls end before they start or last longer than the clock limit; they were left out and counted.")
			}
			if f.Counts.NoStart > 0 {
				st.Reasons = append(st.Reasons, fmt.Sprintf("%d audit documents had no start time and were dropped.", f.Counts.NoStart))
			}
			if off := f.Parts.IISOffsetMs; off != nil && math.Abs(*off) > th.ClockToleranceMs {
				st.Reasons = append(st.Reasons, fmt.Sprintf("The IIS clock and the audit clock differ by about %.1f s, more than the %.0f s allowed for, so onsets from the two sources less than that apart are not put in order.", math.Abs(*off)/1000, th.ClockToleranceMs/1000))
			}
			if s.Flags[SigAuditDip].Value == Yes {
				st.Reasons = append(st.Reasons, "The audit share of IIS requests fell in the incident window, so some calls may be missing from the audit.")
			}
		}
		if in.Audit == AuditPresent {
			st.Findings = append(st.Findings, fmt.Sprintf("%d documents read, %d with no start time, %d not completed, %d with a clock anomaly.", f.Counts.Read, f.Counts.NoStart, f.Counts.Incomplete, f.Counts.Anomalous))
		}
		if f.Coverage.IncShare != nil && f.Coverage.RefShare != nil {
			st.Findings = append(st.Findings, fmt.Sprintf("Audit share of IIS requests: %.2f in the reference window, %.2f in the incident window.", *f.Coverage.RefShare, *f.Coverage.IncShare))
		}
		if len(f.Gaps) > 0 {
			st.Findings = append(st.Findings, gapNote(f))
		}
	}

	// Step 2: Shape.
	{
		st := &steps[2]
		switch {
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		case f.Onset.Status == "cannot date":
			st.set(CannotTestYet, f.Onset.Why)
		default:
			st.set(Ready, "")
			if len(f.Gaps) > 0 {
				st.degrade(PartlyReady, gapNote(f))
				st.UntestedSpans = longGaps(f)
			}
			if referenceAfter(in) {
				st.degrade(PartlyReady, "The reference window comes after the incident window.")
			}
			if f.Onset.Basis == "outside time" {
				st.degrade(PartlyReady, "The audit durations did not move. The start is dated from outside time, so the shape and the scope, which read audit durations, are not shown.")
			}
		}
		if audit {
			switch f.Onset.Status {
			case "found":
				st.Findings = append(st.Findings, "The change starts at "+fmtT(*f.Onset.T0)+".")
			default:
				st.Findings = append(st.Findings, "Start of the change: "+f.Onset.Status+". "+f.Onset.Why)
			}
			st.Findings = append(st.Findings, "Shape: "+f.Shape.Kind+". "+f.Shape.Why)
			st.Findings = append(st.Findings, "Scope: "+f.Scope.Kind+". "+f.Scope.Why)
		}
	}

	// Step 3: Time split.
	{
		st := &steps[3]
		aT, cT := false, false
		for _, p := range f.Parts.Items {
			switch p.Name {
			case "A":
				aT = p.Tested
			case "C":
				cT = p.Tested
			}
		}
		switch {
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		case !aT && !cT:
			st.set(CannotTestYet, "The audit documents carry neither the audit set-up time nor the action time.")
		default:
			st.set(Ready, "")
			if !f.Parts.OTested {
				st.degrade(PartlyReady, "Outside time was not tested: "+f.Parts.OWhy)
			}
			if f.Parts.OTested && f.Parts.IISMatched > 0 && float64(f.Parts.OClamped) > oClampNoteShare*float64(f.Parts.IISMatched) {
				st.degrade(PartlyReady, fmt.Sprintf("Outside time came out below zero for %d of %d matched calls and was set to zero, so it may be understated.", f.Parts.OClamped, f.Parts.IISMatched))
			}
			if f.Parts.Coverage != nil && *f.Parts.Coverage < th.PartsMinCoverage {
				st.degrade(PartlyReady, "Too few calls carry both parts of the split.")
			}
			if len(f.Gaps) > 0 {
				st.degrade(PartlyReady, gapNote(f))
				st.UntestedSpans = longGaps(f)
			}
		}
		if audit {
			for _, p := range s.Split.Shares {
				switch {
				case !p.Tested:
					st.Findings = append(st.Findings, p.Name+": not tested.")
				case p.Share == nil:
					st.Findings = append(st.Findings, p.Name+": no share of the extra time.")
				default:
					st.Findings = append(st.Findings, fmt.Sprintf("%s: share of the extra time %.2f.", p.Name, *p.Share))
				}
			}
			if f.Parts.Dominant != "" {
				st.Findings = append(st.Findings, "The part that grew most is "+f.Parts.Dominant+".")
			} else if f.Parts.DominantWhy != "" {
				st.Findings = append(st.Findings, f.Parts.DominantWhy)
			}
			if s.Split.Note != "" {
				st.Findings = append(st.Findings, s.Split.Note)
			}
		}
	}

	// Step 4: Load.
	{
		st := &steps[4]
		switch {
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		case f.Load.IncWms == nil:
			st.set(CannotTestYet, "Too few calls to read the time each call takes.")
		default:
			st.set(Ready, "")
			if in.PoolLimit == nil {
				st.degrade(PartlyReady, "No pool limit was entered, so load cannot be set against a limit.")
			}
			if len(f.Gaps) > 0 {
				st.degrade(PartlyReady, gapNote(f))
				st.UntestedSpans = longGaps(f)
			}
		}
		if audit {
			st.Findings = append(st.Findings, "Reading: "+f.Load.Reading+".")
			if f.Load.PlannedL != nil {
				st.Findings = append(st.Findings, fmt.Sprintf("Planning load is %.1f requests in flight at %.0f requests per second.", *f.Load.PlannedL, f.Load.PlanningPerSec))
			}
			if s.Pool.Why != "" {
				st.Findings = append(st.Findings, s.Pool.Why)
			}
		}
	}

	// Step 5: Changes.
	{
		st := &steps[5]
		switch {
		case !in.ReleasesEntered:
			st.set(CannotTestYet, "No change list was entered, so changes cannot be placed against the start.")
		case f.Onset.Status != "found":
			st.set(PartlyReady, "The start of the change was not dated, so listed changes cannot be placed before or after it.")
		default:
			st.set(Ready, "")
		}
		if in.ReleasesEntered {
			st.Reasons = append(st.Reasons, ReleaseCaveat)
			st.Findings = append(st.Findings, s.Timeline.Why)
			for _, it := range s.Timeline.Items {
				st.Findings = append(st.Findings, fmtT(it.At)+" "+it.Label+": "+it.Relation+".")
			}
		}
	}

	// Step 6: App tier. S1, S2, S3 and S5 are read from the audit and IIS; S4, S6
	// and S7 need a lane.
	{
		st := &steps[6]
		if !audit {
			st.set(CannotTestYet, noAuditReason(in))
		} else {
			laneIDs := []string{"S4", "S6", "S7"}
			known := 0
			for _, id := range laneIDs {
				if v, ok := in.Lanes[id]; ok && v != Unknown {
					known++
				}
			}
			st.set(Ready, "")
			if known < len(laneIDs) {
				st.degrade(PartlyReady, "S4, S6 and S7 need data the audit does not hold (driver pool events, outbound call timings, filter timings), so they were not tested.")
			}
			if !f.Parts.OTested {
				st.degrade(PartlyReady, "S1 and S2 read outside time, which could not be tested.")
			}
			if in.PoolLimit == nil {
				st.degrade(PartlyReady, "S3 needs the pool limit, which was not entered.")
			}
			st.Findings = append(st.Findings, "Tested from the audit and IIS: S1, S2, S3, S5. Held for other sources: S4, S6, S7.")
		}
	}

	// Steps 7 to 9: SQL, Mongo, Host. Only a lane's own data can read them.
	for _, l := range []struct {
		n     int
		group string
		src   string
	}{{7, GroupD, "SQL Server"}, {8, GroupM, "MongoDB"}, {9, GroupH, "host"}} {
		st := &steps[l.n]
		entries := laneGroup(l.group)
		known, total, grey := laneCount(in, entries)
		switch {
		case known == 0:
			st.set(CannotTestYet, "No "+l.src+" data is imported for this case.")
		case known < total-grey:
			st.set(PartlyReady, fmt.Sprintf("%d of %d suspects have a reading from the %s data.", known, total, l.src))
		default:
			st.set(Ready, "")
		}
		if known > 0 && grey > 0 {
			st.degrade(PartlyReady, greyReason(grey))
		}
		st.Findings = append(st.Findings, fmt.Sprintf("Suspects with a reading: %d of %d.", known, total))
	}

	// Host lane gaps (A35): any gap lowers Step 9 to at most Partly ready.
	laneGapStep(&steps[9], "host", in.LaneGaps["host"])

	// Step 10: Origin.
	{
		st := &steps[10]
		// The step follows the origin the headline reports, so the two cannot
		// disagree: a named origin or "no clear origin" is an answer, and only "not
		// enough data" is Cannot test yet.
		origin := ComputeOrigin(in, f, s, th)
		switch {
		case !audit:
			st.set(CannotTestYet, noAuditReason(in))
		case origin.Status == OriginNotEnough:
			st.set(CannotTestYet, origin.Note)
		default:
			st.set(Ready, "")
			for _, n := range []int{7, 8, 9} {
				if steps[n].Readiness != Ready {
					st.degrade(PartlyReady, "The "+steps[n].Name+" tier was not fully tested, so the origin is read from the tiers that could be dated.")
				}
			}
		}
	}

	// A frame that failed leaves nothing to read: no later step is better than
	// Cannot test yet.
	if steps[0].Readiness == Failed {
		for i := 1; i < StepCount; i++ {
			if steps[i].Readiness == Ready || steps[i].Readiness == PartlyReady {
				steps[i].Readiness = CannotTestYet
				steps[i].Reasons = append(steps[i].Reasons, "The windows in Step 0 are not usable.")
			}
		}
	}
	for i := range steps {
		steps[i].Reasons = dedupe(steps[i].Reasons)
	}
	return steps
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// IsReadinessWord reports whether r is one of the four fixed words.
func IsReadinessWord(r Readiness) bool {
	switch r {
	case Ready, PartlyReady, CannotTestYet, Failed:
		return true
	}
	return false
}

// oClampNoteShare is the share of matched pairs with a negative outside time (set
// to zero) above which Step 3 says so. It only adds a note; it moves no colour.
const oClampNoteShare = 0.25
