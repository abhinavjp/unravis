package investigate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Origin statuses.
const (
	OriginNamed       = "named"
	OriginNone        = "no clear origin"
	OriginNotEnough   = "not enough data"
	RoleOrigin        = "Origin"
	RoleVictim        = "Victim"
	RoleAmplifier     = "Amplifier"
	RoleNotDated      = "Not dated"
	RoleTogether      = "Moved together"
	ConfidenceHigh    = "High"
	ConfidenceMedium  = "Medium"
	ConfidenceLow     = "Low"
	ConfidenceNone    = "None"
	tierLoad          = "load"
	genericOverturn   = "Import a source with its own clock for the tier that moved first, and see whether it moved at the same time."
	noOriginClockNote = "Onsets closer together than one minute plus the clock tolerance between hosts cannot be put in order."
)

// TierRole is one tier on the origin chart.
type TierRole struct {
	Tier  string     `json:"tier"`
	Label string     `json:"label"`
	Role  string     `json:"role"`
	At    *time.Time `json:"at,omitempty"`
	Why   string     `json:"why"`
}

// Origin is Step 10: which tier moved first. It never names a cause; it says what
// moved first and what the data cannot separate.
type Origin struct {
	Status string     `json:"status"`
	Tier   string     `json:"tier,omitempty"`
	Label  string     `json:"label,omitempty"`
	At     *time.Time `json:"at,omitempty"`
	Roles  []TierRole `json:"roles"`
	Note   string     `json:"note"`
	// ClockNote states the clock difference the ordering allows for.
	ClockNote string `json:"clockNote"`
}

// Confidence is the "How sure are we" panel. Word is High, Medium, Low or None.
type Confidence struct {
	Word           string   `json:"word"`
	Why            string   `json:"why"`
	SourcesUsed    []string `json:"sourcesUsed"`
	SourcesMissing []string `json:"sourcesMissing"`
	NotTested      []string `json:"notTested"`
	Overturn       string   `json:"overturn"`
}

var tierLabels = map[string]string{
	tierLoad: "Arrival rate",
	"a":      "Audit set-up (A)",
	"c":      "Action time (C)",
	"o":      "Outside time (O)",
	"sql":    "SQL Server",
	"mongo":  "MongoDB",
	"host":   "Host",
}

func tierLabel(t string) string {
	if l, ok := tierLabels[t]; ok {
		return l
	}
	return t
}

// ComputeOrigin orders the tiers by when their own metric first moved (A24, with
// the guard of rule 16) and says which moved first, which followed, and which
// amplified the slowness.
func ComputeOrigin(in Input, f Facts, s Signals, th Thresholds) Origin {
	o := Origin{Roles: []TierRole{}}
	sep := time.Duration(th.OriginBucketSeconds)*time.Second + time.Duration(th.ClockToleranceMs*float64(time.Millisecond))
	o.ClockNote = fmt.Sprintf("Hosts may differ by up to %.0f s, and onsets are read by the minute, so onsets less than %.0f s apart are not put in order.", th.ClockToleranceMs/1000, sep.Seconds())

	type dated struct {
		tier string
		at   time.Time
	}
	var ds []dated
	near := map[string]bool{}
	for _, t := range f.Tiers {
		if t.At != nil {
			ds = append(ds, dated{t.Tier, *t.At})
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].at.Before(ds[j].at) })

	switch {
	case in.Audit != AuditPresent || f.Onset.Status != "found":
		o.Status = OriginNotEnough
		o.Note = "There is no dated start of the change, so tiers cannot be put in order."
	case len(ds) == 1 && othersHeldFlat(f, ds[0].tier):
		// Every other tier's own data was read in every incident minute and held
		// flat, so the one that moved is the first to move.
		o.Status = OriginNamed
		o.Tier, o.Label, o.At = ds[0].tier, tierLabel(ds[0].tier), ptrTime(ds[0].at)
		o.Note = o.Label + " moved first, at " + fmtT(ds[0].at) + ". It was the only tier that moved; the others were read and held flat."
	case len(ds) < 2:
		o.Status = OriginNotEnough
		if len(ds) == 1 {
			o.Note = "Only " + tierLabel(ds[0].tier) + " could be dated, and another tier could not be read in every minute, so no origin is named."
		} else {
			o.Note = "No tier could be dated, so no origin is named."
		}
	default:
		first := ds[0]
		together := 0
		for _, d := range ds {
			if d.at.Sub(first.at) < sep {
				together++
				near[d.tier] = true
			}
		}
		if together > 1 {
			o.Status = OriginNone
			o.Note = "Several tiers moved at about the same time, so the data does not show which moved first. " + noOriginClockNote
		} else if unread := unreadTiers(f); len(unread) > 0 {
			// A tier whose own data was not read in every minute could have moved
			// before the first dated one, so nothing is named (as for a lone tier).
			o.Status = OriginNotEnough
			o.Note = tierLabel(first.tier) + " is the earliest tier that could be dated, but " + joinLabels(unread) + " could not be read in every minute, so one of them may have moved earlier and no origin is named."
		} else {
			o.Status = OriginNamed
			o.Tier, o.Label, o.At = first.tier, tierLabel(first.tier), ptrTime(first.at)
			o.Note = o.Label + " moved first, at " + fmtT(first.at) + "."
		}
	}

	arrivals := s.Flags[SigArrivalsUp].Value == Yes
	for _, t := range f.Tiers {
		r := TierRole{Tier: t.Tier, Label: tierLabel(t.Tier), At: t.At}
		switch {
		case t.At == nil:
			r.Role = RoleNotDated
			r.Why = t.Why
		case o.Status == OriginNamed && t.Tier == o.Tier:
			r.Role = RoleOrigin
			r.Why = "Moved first."
		case o.Status == OriginNamed && t.Tier == tierLoad && arrivals:
			r.Role = RoleAmplifier
			r.Why = "Arrivals rose by the change mark after the first tier moved."
		case o.Status == OriginNamed:
			r.Role = RoleVictim
			r.Why = "Moved after the first tier."
		case o.Status == OriginNone && near[t.Tier]:
			r.Role = RoleTogether
			r.Why = "Moved within the time that cannot be told apart from the earliest."
		case o.Status == OriginNone:
			r.Role = RoleVictim
			r.Why = "Moved after the earliest tiers."
		default:
			r.Role = RoleNotDated
			r.Why = "Not put in order: " + o.Note
		}
		o.Roles = append(o.Roles, r)
	}
	return o
}

func ptrTime(t time.Time) *time.Time { return &t }

// othersHeldFlat is true when every tier except the named one has its own data
// read in every incident minute and did not move.
func othersHeldFlat(f Facts, tier string) bool {
	for _, t := range f.Tiers {
		if t.Tier == tier {
			continue
		}
		if t.At != nil || !t.Tested {
			return false
		}
	}
	return true
}

// ComputeConfidence builds the "How sure are we" panel. Audit alone gives Low at
// best, audit with IIS gives Medium, and High needs the other tiers' own data as
// well. A clock anomaly or a long gap lowers it one step (never below Low once an
// answer exists).
func ComputeConfidence(in Input, f Facts, s Signals, o Origin, sps []Suspect, steps []Step) Confidence {
	c := Confidence{SourcesUsed: []string{}, SourcesMissing: []string{}, NotTested: []string{}}
	audit := hasAudit(in, f)
	if audit {
		c.SourcesUsed = append(c.SourcesUsed, "audit")
	} else {
		c.SourcesMissing = append(c.SourcesMissing, "audit")
	}
	if f.Parts.OTested {
		c.SourcesUsed = append(c.SourcesUsed, "IIS logs")
	} else {
		c.SourcesMissing = append(c.SourcesMissing, "IIS logs")
	}
	for _, l := range []struct {
		n    int
		name string
	}{{7, "SQL Server data"}, {8, "MongoDB data"}, {9, "host counters"}} {
		if steps[l.n].Readiness == CannotTestYet {
			c.SourcesMissing = append(c.SourcesMissing, l.name)
		} else {
			c.SourcesUsed = append(c.SourcesUsed, l.name)
		}
	}
	if in.ReleasesEntered {
		c.SourcesUsed = append(c.SourcesUsed, "change list")
	} else {
		c.SourcesMissing = append(c.SourcesMissing, "change list")
	}

	var top *Suspect
	for i := range sps {
		if sps[i].Result == ResCannotTest || sps[i].Colour == Grey {
			c.NotTested = append(c.NotTested, sps[i].ID)
		}
		if top == nil && sps[i].Group != GroupQ && (sps[i].Colour == Red || sps[i].Colour == Orange) {
			top = &sps[i]
		}
	}
	sort.Strings(c.NotTested)

	level := 0 // 0 none, 1 low, 2 medium, 3 high
	switch o.Status {
	case OriginNamed, OriginNone:
		level = 1
		if o.Status == OriginNamed {
			if f.Parts.OTested {
				level = 2
			}
			laneSteps := 0
			for _, n := range []int{7, 8, 9} {
				if steps[n].Readiness != CannotTestYet {
					laneSteps++
				}
			}
			if f.Parts.OTested && laneSteps >= 1 {
				level = 3
			}
		}
	}
	var notes []string
	if level > 1 && (f.Clock.Anomaly == Yes) {
		level--
		notes = append(notes, "Some calls had clock anomalies.")
	}
	if level > 1 && len(longGaps(f)) > 0 {
		level--
		notes = append(notes, "The data has gaps that could not be tested.")
	}
	c.Word = [...]string{ConfidenceNone, ConfidenceLow, ConfidenceMedium, ConfidenceHigh}[level]
	switch {
	case level == 0:
		c.Why = "No answer is given: " + o.Note
	case o.Status == OriginNone:
		c.Why = "The tiers moved together, so the answer is that no origin can be named from this data."
	case !f.Parts.OTested:
		c.Why = "Only the audit was used. Adding the IIS logs would let outside time be tested."
	case level == 2:
		c.Why = "The audit and the IIS logs were used. Adding the SQL Server, MongoDB or host data would test the other tiers."
	default:
		c.Why = "The audit, the IIS logs and at least one other tier's own data were used."
	}
	for _, n := range notes {
		c.Why += " " + n
	}
	switch {
	case top != nil && top.Overturn != "":
		c.Overturn = top.Overturn
	default:
		c.Overturn = genericOverturn
	}
	return c
}

// Headline is one plain sentence for the top of the result.
func Headline(f Facts, o Origin, sps []Suspect) string {
	switch {
	case f.Counts.Used == 0:
		return "There is no audit data to read in these windows."
	case f.Onset.Status == "not found" && !(f.Change.Known && f.Change.Meaningful):
		return "No meaningful slowdown was found: the incident window is not clearly slower than the reference window."
	case f.Onset.Status == "cannot date":
		return "The slowdown could not be dated from this data. " + f.Onset.Why
	case f.Onset.T0 == nil:
		return "A slowdown was measured, but it did not stay high long enough to be dated."
	}
	h := "The change starts at " + fmtT(*f.Onset.T0) + "."
	switch o.Status {
	case OriginNamed:
		h += " " + o.Label + " moved first."
	case OriginNone:
		h += " The tiers moved together, so there is no clear origin."
	default:
		h += " Too few tiers could be dated to name an origin."
	}
	return h
}

// unreadTiers lists the tiers with no onset whose own data was not read in every
// incident minute: any of them might have moved first.
func unreadTiers(f Facts) []string {
	var out []string
	for _, t := range f.Tiers {
		if t.At == nil && !t.Tested {
			out = append(out, t.Tier)
		}
	}
	return out
}

func joinLabels(tiers []string) string {
	names := make([]string, len(tiers))
	for i, t := range tiers {
		names[i] = tierLabel(t)
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
