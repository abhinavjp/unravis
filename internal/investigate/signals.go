package investigate

import (
	"fmt"
	"sort"
	"time"
)

// Signal names. Each is yes, no or unknown (A27: facts, then signals, then the
// evaluation). The catalog refers to these names and to nothing else.
const (
	SigArrivalsUp    = "arrivals_up"     // more calls per second than the reference
	SigP95Up         = "p95_up"          // the P95 rose by the change mark
	SigP50Flat       = "p50_flat"        // the median stayed below the change mark
	SigScopeNarrow   = "scope_narrow"    // one route or a few
	SigScopeAll      = "scope_all"       // nearly every route
	SigRetriesUp     = "retries_up"      // retried calls rose by the rate mark
	SigErrorsPrecede = "errors_precede"  // the first server error is at or before t0
	SigALarge        = "a_large"         // A rose by the change mark
	SigCLarge        = "c_large"         // C rose by the change mark
	SigPLarge        = "p_large"         // P rose by the change mark
	SigOLarge        = "o_large"         // O rose by the change mark
	SigOOrALarge     = "o_or_a_large"    // O or A rose by the change mark
	SigPoolWouldFill = "pool_would_fill" // lambda x W reaches the entered pool limit
	SigLAtLimit      = "l_at_limit"      // in flight sits at the limit, or lambda x W reaches it
	SigClockAnomaly  = "clock_anomaly"   // calls end before they start
	SigAuditDip      = "audit_dip"       // the audit share of IIS requests fell
	SigChangesLead   = "changes_lead"    // a listed change sits just before t0
)

// Signal is one yes, no or unknown with the reason, and, when unknown for want of
// a source, which source that is ("audit", "iis", "pool limit", "releases").
type Signal struct {
	Value   Tri    `json:"value"`
	Missing string `json:"missing,omitempty"`
	Why     string `json:"why"`
}

// PartShare is one line of the Step 3 shares of the extra time.
type PartShare struct {
	Name       string   `json:"name"`
	Share      *float64 `json:"share,omitempty"`
	Meaningful bool     `json:"meaningful"`
	Tested     bool     `json:"tested"`
}

// Split is Step 3: shares of the extra time and the part that holds most of it.
type Split struct {
	Shares   []PartShare `json:"shares"`
	Dominant string      `json:"dominant"`
	Note     string      `json:"note,omitempty"`
}

// PoolReading is Step 4: load against the pool limit the user entered.
type PoolReading struct {
	Entered     bool     `json:"entered"`
	Limit       *int     `json:"limit,omitempty"`
	PlannedL    *float64 `json:"plannedL,omitempty"`
	PlannedFill *float64 `json:"plannedFill,omitempty"`
	WouldFill   Tri      `json:"wouldFill"`
	AtLimit     Tri      `json:"atLimit"`
	Reading     string   `json:"reading"`
	Why         string   `json:"why"`
}

// ChangeItem is one typed change placed against t0.
type ChangeItem struct {
	At            time.Time `json:"at"`
	Label         string    `json:"label"`
	Relation      string    `json:"relation"`
	MinutesBefore *int      `json:"minutesBefore,omitempty"`
}

// ChangeTimeline is Step 5. It is built only from the changes the user typed.
type ChangeTimeline struct {
	Entered bool         `json:"entered"`
	Items   []ChangeItem `json:"items"`
	Leading []string     `json:"leading,omitempty"`
	Verdict Tri          `json:"verdict"`
	Why     string       `json:"why"`
	Caveat  string       `json:"caveat"`
}

// ReleaseCaveat is shown wherever the typed release list is the only evidence.
const ReleaseCaveat = "The release list is typed by you, so a release that was not entered cannot be ruled out."

// Signals are the yes, no or unknown readings the evaluation uses.
type Signals struct {
	Flags    map[string]Signal `json:"flags"`
	Split    Split             `json:"split"`
	Pool     PoolReading       `json:"pool"`
	Timeline ChangeTimeline    `json:"changes"`
}

func sig(v Tri, why string) Signal { return Signal{Value: v, Why: why} }

func missing(src, why string) Signal { return Signal{Value: Unknown, Missing: src, Why: why} }

func yn(b bool) Tri {
	if b {
		return Yes
	}
	return No
}

// ComputeSignals reads the facts and the thresholds and returns the signals. It
// reads no data of its own.
func ComputeSignals(in Input, f Facts, th Thresholds) Signals {
	s := Signals{Flags: map[string]Signal{}}
	fl := s.Flags
	auditGone := in.Audit != AuditPresent

	switch {
	case auditGone || f.Counts.Used == 0:
		for _, n := range []string{SigArrivalsUp, SigP95Up, SigP50Flat, SigScopeNarrow, SigScopeAll, SigRetriesUp, SigErrorsPrecede, SigALarge, SigCLarge, SigPLarge, SigClockAnomaly} {
			fl[n] = missing("audit", "There is no audit data for these windows.")
		}
	default:
		switch f.Load.LambdaDx {
		case "up":
			fl[SigArrivalsUp] = sig(Yes, "Calls per second rose by the rate mark.")
		case "unknown":
			fl[SigArrivalsUp] = missing("audit", "Calls per second could not be compared.")
		default:
			fl[SigArrivalsUp] = sig(No, "Calls per second did not rise by the rate mark.")
		}
		if !f.Change.Known {
			fl[SigP95Up] = missing("audit", "Not enough calls to compare the P95.")
		} else if f.Change.Meaningful && changeDirection(f.Change) == "up" {
			fl[SigP95Up] = sig(Yes, "The P95 rose by the change mark.")
		} else {
			fl[SigP95Up] = sig(No, "The P95 did not rise by the change mark.")
		}
		if !f.Change.P50.Known {
			fl[SigP50Flat] = missing("audit", "Not enough calls to compare the median.")
		} else {
			fl[SigP50Flat] = sig(yn(!f.Change.P50.Meaningful), "Compared with the change mark.")
		}
		switch f.Scope.Kind {
		case "one route", "a few routes":
			fl[SigScopeNarrow] = sig(Yes, f.Scope.Why)
			fl[SigScopeAll] = sig(No, f.Scope.Why)
		case "many routes":
			fl[SigScopeNarrow] = sig(No, f.Scope.Why)
			fl[SigScopeAll] = sig(No, f.Scope.Why)
		case "everything":
			fl[SigScopeNarrow] = sig(No, f.Scope.Why)
			fl[SigScopeAll] = sig(Yes, f.Scope.Why)
		default:
			fl[SigScopeNarrow] = missing("audit", f.Scope.Why)
			fl[SigScopeAll] = missing("audit", f.Scope.Why)
		}
		if !f.Retries.Known {
			fl[SigRetriesUp] = missing("audit", "The audit documents do not say which client made each call.")
		} else {
			fl[SigRetriesUp] = sig(yn(f.Retries.Direction == "up"), "Retried calls compared with the reference.")
		}
		switch {
		case !f.Errors.Known:
			fl[SigErrorsPrecede] = missing("audit", "The audit documents carry no outcome.")
		case f.Errors.IncServer == 0:
			fl[SigErrorsPrecede] = sig(No, "No server error in the incident window.")
		case f.Onset.T0 == nil:
			fl[SigErrorsPrecede] = missing("audit", "The start of the change could not be dated.")
		default:
			fl[SigErrorsPrecede] = sig(yn(!minuteOf(*f.Errors.FirstIncServer).After(*f.Onset.T0)), "First server error compared with t0, by the minute.")
		}
		for _, p := range f.Parts.Items {
			name := map[string]string{"A": SigALarge, "C": SigCLarge, "P": SigPLarge, "O": SigOLarge}[p.Name]
			switch {
			case !p.Tested && p.Name == "O":
				fl[name] = missing("iis", p.Why)
			case !p.Tested:
				fl[name] = missing("audit", p.Why)
			default:
				fl[name] = sig(yn(p.Change.Meaningful && p.Change.Direction == "up"), "Mean of this part compared with the change mark.")
			}
		}
		fl[SigClockAnomaly] = Signal{Value: f.Clock.Anomaly, Why: "Calls that end before they start, or last longer than the clock limit."}
		if f.Clock.Anomaly == Unknown {
			fl[SigClockAnomaly] = missing("audit", "No call has both a start and a completion.")
		}
	}

	a, o := fl[SigALarge], fl[SigOLarge]
	switch {
	case a.Value == Yes || o.Value == Yes:
		fl[SigOOrALarge] = sig(Yes, "Audit set-up or outside time rose by the change mark.")
	case a.Value == No && o.Value == No:
		fl[SigOOrALarge] = sig(No, "Neither audit set-up nor outside time rose by the change mark.")
	default:
		src := o.Missing
		if src == "" {
			src = a.Missing
		}
		fl[SigOOrALarge] = missing(src, "One of the two could not be tested.")
	}

	switch f.Coverage.Dip {
	case Yes:
		fl[SigAuditDip] = sig(Yes, "The audit share of IIS requests fell by the dip mark.")
	case No:
		fl[SigAuditDip] = sig(No, "The audit share of IIS requests held.")
	default:
		fl[SigAuditDip] = missing("iis", "Needs IIS rows with enough requests in both windows.")
	}

	s.Split = computeSplit(f)
	s.Pool = computePool(f, th)
	pm := ""
	if s.Pool.WouldFill == Unknown {
		pm = poolMissing(in)
	}
	fl[SigPoolWouldFill] = Signal{Value: s.Pool.WouldFill, Missing: pm, Why: s.Pool.Why}
	fl[SigLAtLimit] = Signal{Value: s.Pool.AtLimit, Missing: pm, Why: s.Pool.Why}
	s.Timeline = computeTimeline(in, f, th)
	fl[SigChangesLead] = Signal{Value: s.Timeline.Verdict, Why: s.Timeline.Why}
	if s.Timeline.Verdict == Unknown && !in.ReleasesEntered {
		fl[SigChangesLead] = missing("releases", s.Timeline.Why)
	}
	return s
}

func poolMissing(in Input) string {
	if in.PoolLimit == nil {
		return "pool limit"
	}
	return "audit"
}

func changeDirection(c OverallShift) string {
	if c.P95.Known {
		return c.P95.Direction
	}
	return c.Mean.Direction
}

func computeSplit(f Facts) Split {
	sp := Split{Dominant: f.Parts.Dominant}
	for _, it := range f.Parts.Items {
		sp.Shares = append(sp.Shares, PartShare{Name: it.Name, Share: it.Share, Meaningful: it.Change.Meaningful, Tested: it.Tested})
	}
	if !f.Parts.OTested {
		sp.Note = "Outside time was not tested, so the shares cover A, C and P only."
	}
	return sp
}

func computePool(f Facts, th Thresholds) PoolReading {
	l := f.Load
	r := PoolReading{WouldFill: Unknown, AtLimit: Unknown, Limit: l.PoolLimit, PlannedL: l.PlannedL, PlannedFill: l.PlannedFill, Entered: l.PoolLimit != nil, Reading: l.Reading}
	switch {
	case l.PoolLimit == nil:
		r.Why = "No pool limit was entered, so load cannot be set against a limit."
	case l.PlannedFill == nil:
		r.Why = "Too few calls to read the time each call takes."
	default:
		full := *l.PlannedFill >= th.PoolFullShare-floatEps
		r.WouldFill = yn(full)
		top := l.FlatTop && float64(l.FlatTopAt) >= th.PoolFullShare*float64(*l.PoolLimit)-floatEps
		r.AtLimit = yn(full || top)
		r.Why = fmt.Sprintf("Planned load is %.1f requests in flight against a limit of %d.", *l.PlannedL, *l.PoolLimit)
	}
	return r
}

func computeTimeline(in Input, f Facts, th Thresholds) ChangeTimeline {
	t := ChangeTimeline{Entered: in.ReleasesEntered, Items: []ChangeItem{}, Verdict: Unknown, Caveat: ReleaseCaveat}
	if !in.ReleasesEntered {
		t.Why = "No change list was entered."
		return t
	}
	rel := append([]Release(nil), in.Releases...)
	sort.SliceStable(rel, func(i, j int) bool { return rel[i].At.Before(rel[j].At) })
	var t0 *time.Time
	if f.Onset.Status == "found" {
		t0 = f.Onset.T0
	}
	lead := time.Duration(th.ChangeLeadMinutes) * time.Minute
	for _, r := range rel {
		it := ChangeItem{At: r.At.UTC(), Label: r.Label}
		switch {
		case t0 == nil:
			it.Relation = "no start time to compare with"
		case r.At.After(*t0):
			it.Relation = "after the start"
		case t0.Sub(r.At) <= lead:
			it.Relation = "just before the start"
			m := int(t0.Sub(r.At) / time.Minute)
			it.MinutesBefore = &m
			t.Leading = append(t.Leading, r.Label)
		default:
			it.Relation = "long before the start"
			m := int(t0.Sub(r.At) / time.Minute)
			it.MinutesBefore = &m
		}
		t.Items = append(t.Items, it)
	}
	switch {
	case t0 == nil:
		t.Why = "The start of the change was not dated, so listed changes cannot be placed before or after it."
	case len(t.Leading) > 0:
		t.Verdict = Yes
		t.Why = "A listed change sits within the lead window before the start."
	case len(rel) == 0:
		t.Verdict = No
		t.Why = "No change listed."
	default:
		t.Verdict = No
		t.Why = "No listed change sits within the lead window before the start."
	}
	return t
}
