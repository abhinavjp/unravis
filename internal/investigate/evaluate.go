package investigate

import (
	"fmt"
	"sort"
	"time"
)

// SuspectResult is how the data stands against one catalog entry. It is never a
// score: the four words are fixed (research/investigation-walkthrough.md, section 4).
type SuspectResult string

const (
	ResConsistent SuspectResult = "Consistent"
	ResPartly     SuspectResult = "Partly consistent"
	ResContra     SuspectResult = "Contradicted"
	ResCannotTest SuspectResult = "Cannot test yet"
)

// Colour is the suspect colour. Red is most likely, orange is likely, green is
// ruled out by the data we have, and grey is not sure: could not be tested, or
// does not apply. Grey is never a pass.
type Colour string

const (
	Red    Colour = "red"
	Orange Colour = "orange"
	Green  Colour = "green"
	Grey   Colour = "grey"
)

// SuspectEvidence is one line behind a suspect card: a signal and what it read.
type SuspectEvidence struct {
	Signal string `json:"signal"`
	Role   string `json:"role"` // "must", "expect" or "lane"
	Value  Tri    `json:"value"`
	Why    string `json:"why"`
}

// Suspect is one catalog entry, evaluated.
type Suspect struct {
	ID       string            `json:"id"`
	Group    string            `json:"group"`
	Title    string            `json:"title"`
	Result   SuspectResult     `json:"result"`
	Colour   Colour            `json:"colour"`
	Why      string            `json:"why"`
	Tier     string            `json:"tier,omitempty"`
	OnsetAt  *time.Time        `json:"onsetAt,omitempty"`
	Evidence []SuspectEvidence `json:"evidence"`
	// NextCheck is the cheapest next check; Overturn is what would overturn the
	// reading; Needs is what to import or enable when the entry cannot be tested.
	NextCheck string `json:"nextCheck"`
	Overturn  string `json:"overturn"`
	Needs     string `json:"needs,omitempty"`
	Rank      int    `json:"rank"`
	// Order is the position in the catalog, kept so the sort is reproducible.
	Order int `json:"-"`
	cost  int
}

func stateRank(r SuspectResult) int {
	switch r {
	case ResConsistent:
		return 0
	case ResPartly:
		return 1
	case ResCannotTest:
		return 2
	}
	return 3
}

// checkCost orders the cheapest check first: the audit alone, then the audit with
// IIS or typed input, then another lane's data.
func checkCost(e CatalogEntry) int {
	switch {
	case e.LaneRequired:
		return 2
	case e.Needs == "":
		return 0
	}
	return 1
}

// tierOnset finds the onset of a tier in the facts. S2 reads the earlier of the
// outside-time and audit set-up onsets.
func tierOnset(f Facts, e CatalogEntry) (*time.Time, string) {
	find := func(tier string) *time.Time {
		for _, t := range f.Tiers {
			if t.Tier == tier {
				return t.At
			}
		}
		return nil
	}
	if e.ID == "S2" {
		o, a := find("o"), find("a")
		switch {
		case o == nil && a == nil:
			return nil, "o"
		case o == nil:
			return a, "a"
		case a == nil:
			return o, "o"
		case a.Before(*o):
			return a, "a"
		}
		return o, "o"
	}
	if e.Tier == "" {
		return nil, ""
	}
	return find(e.Tier), e.Tier
}

// Evaluate reads the catalog against the signals and returns every suspect with
// a result, a colour and its cheapest next check, in the fixed order: state
// group, earliest tier onset, cheapest check, catalog id. Unknown is never no:
// missing data cannot contradict a suspect.
func Evaluate(in Input, f Facts, s Signals, th Thresholds) []Suspect {
	cat := Catalog()
	out := make([]Suspect, 0, len(cat))
	for i, e := range cat {
		sp := Suspect{ID: e.ID, Group: e.Group, Title: e.Title, Tier: e.Tier, NextCheck: e.Check, Overturn: e.Overturn, Needs: e.Needs, Order: i, cost: checkCost(e), Evidence: []SuspectEvidence{}}
		sp.OnsetAt, sp.Tier = tierOnset(f, e)
		evaluateOne(&sp, e, in, s)
		out = append(out, sp)
	}
	colour(out, in, f, th)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.Result), stateRank(b.Result); ra != rb {
			return ra < rb
		}
		switch {
		case a.OnsetAt != nil && b.OnsetAt == nil:
			return true
		case a.OnsetAt == nil && b.OnsetAt != nil:
			return false
		case a.OnsetAt != nil && b.OnsetAt != nil && !a.OnsetAt.Equal(*b.OnsetAt):
			return a.OnsetAt.Before(*b.OnsetAt)
		}
		if a.cost != b.cost {
			return a.cost < b.cost
		}
		return a.Order < b.Order
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

func sigOf(s Signals, name string) Signal {
	if v, ok := s.Flags[name]; ok {
		return v
	}
	return Signal{Value: Unknown, Missing: "audit", Why: "This signal was not computed."}
}

func (sp *Suspect) add(signal, role string, v Signal) {
	sp.Evidence = append(sp.Evidence, SuspectEvidence{Signal: signal, Role: role, Value: v.Value, Why: v.Why})
}

func (sp *Suspect) set(r SuspectResult, c Colour, why string) {
	sp.Result, sp.Colour, sp.Why = r, c, why
}

func evaluateOne(sp *Suspect, e CatalogEntry, in Input, s Signals) {
	laneVal, haveLane := in.Lanes[e.ID]
	if haveLane && laneVal == Unknown {
		haveLane = false
	}
	if haveLane {
		sp.Evidence = append(sp.Evidence, SuspectEvidence{Signal: "lane", Role: "lane", Value: laneVal, Why: "What this suspect's own data source shows."})
	}

	// Held grey in Release 1 (A26): no reliable test yet, whatever a lane says.
	if e.GreyR1 {
		sp.set(ResCannotTest, Grey, "Held grey in this release: there is no reliable test for it yet.")
		return
	}

	switch e.ID {
	case "Q1":
		a := sigOf(s, SigClockAnomaly)
		sp.add(SigClockAnomaly, "must", a)
		switch {
		case haveLane && laneVal == No:
			sp.set(ResContra, Green, "A second source agrees with the audit clock.")
		case a.Value == Yes || (haveLane && laneVal == Yes):
			sp.set(ResConsistent, Orange, "Some calls end before they start or last longer than the clock limit. Fix the data first: the numbers in the other steps may be unreliable.")
		case a.Value == No:
			sp.set(ResPartly, Grey, "No call ends before it starts, but a time-zone or clock-offset error between sources can only be checked against a second source.")
		default:
			sp.set(ResCannotTest, Grey, "There is no audit data to check the clock on.")
		}
		return
	case "Q2":
		a := sigOf(s, SigAuditDip)
		sp.add(SigAuditDip, "must", a)
		switch a.Value {
		case Yes:
			sp.set(ResConsistent, Orange, "The audit share of IIS requests fell in the incident window, so some calls may be missing from the audit.")
		case No:
			sp.set(ResContra, Green, "The audit share of IIS requests held between the two windows.")
		default:
			sp.set(ResCannotTest, Grey, "Needs IIS rows with enough requests in both windows.")
		}
		return
	case "C1":
		a := sigOf(s, SigChangesLead)
		sp.add(SigChangesLead, "must", a)
		switch a.Value {
		case Yes:
			sp.set(ResConsistent, "", "A listed change sits just before the start. "+ReleaseCaveat)
		case No:
			sp.set(ResPartly, Grey, "No listed change sits just before the start. "+ReleaseCaveat)
		default:
			sp.set(ResCannotTest, Grey, "No change list was entered, or the start of the change was not dated.")
		}
		return
	}

	// Generic rule, in order.
	mustNo, mustUnknownMissing, mustUnknownOther := "", "", ""
	for _, n := range e.Must {
		v := sigOf(s, n)
		sp.add(n, "must", v)
		switch {
		case v.Value == No && mustNo == "":
			mustNo = v.Why
		case v.Value == Unknown && v.Missing != "" && mustUnknownMissing == "":
			mustUnknownMissing = fmt.Sprintf("%s (%s)", v.Why, missingWord(v.Missing))
		case v.Value == Unknown && v.Missing == "" && mustUnknownOther == "":
			mustUnknownOther = v.Why
		}
	}
	expectNo := ""
	for _, n := range e.Expect {
		v := sigOf(s, n)
		sp.add(n, "expect", v)
		if v.Value == No && expectNo == "" {
			expectNo = v.Why
		}
	}
	switch {
	case haveLane && laneVal == No:
		sp.set(ResContra, Green, "This suspect's own data source shows evidence against it.")
	case mustNo != "":
		sp.set(ResContra, Green, "A reading it needs is absent: "+mustNo)
	case e.LaneRequired && !haveLane:
		sp.set(ResCannotTest, Grey, "The audit cannot test this suspect. "+needsLine(e))
	case mustUnknownMissing != "":
		sp.set(ResCannotTest, Grey, "Missing data: "+mustUnknownMissing)
	case mustUnknownOther != "":
		sp.set(ResPartly, Grey, "A reading it needs is unknown: "+mustUnknownOther)
	case expectNo != "":
		sp.set(ResPartly, Orange, "The main reading fits, but one that normally goes with it does not: "+expectNo)
	default:
		sp.set(ResConsistent, "", "Every reading it needs is present.")
	}
}

func needsLine(e CatalogEntry) string {
	if e.Needs == "" {
		return ""
	}
	return "It needs: " + e.Needs
}

func missingWord(src string) string {
	switch src {
	case "iis":
		return "no IIS data"
	case "audit":
		return "no audit data"
	case "pool limit":
		return "no pool limit entered"
	case "releases":
		return "no change list entered"
	}
	return "missing " + src
}

// colour gives each Consistent suspect red or orange. Only the earliest tier among
// the competing suspects is red, and only when that tier is clearly first: tiers
// whose onsets are closer than the bucket plus the clock tolerance cannot be told
// apart (guard 16), so none of them is red. A nothing-to-explain case makes every
// suspect grey. Data-quality suspects (group Q) never compete for red.
func colour(sps []Suspect, in Input, f Facts, th Thresholds) {
	if f.Onset.Status != "found" && !(f.Change.Known && f.Change.Meaningful) {
		// Nothing meaningful to explain: no suspect is ranked.
		for i := range sps {
			if sps[i].Group == GroupQ {
				continue // a data problem matters whether or not there is a slowdown
			}
			sps[i].Colour = Grey
			if sps[i].Result == ResConsistent {
				sps[i].Why += " No meaningful slowdown was found in the window, so this is not ranked."
			}
		}
		return
	}
	sep := time.Duration(th.OriginBucketSeconds)*time.Second + time.Duration(th.ClockToleranceMs*float64(time.Millisecond))
	var comp []int
	for i := range sps {
		if sps[i].Result == ResConsistent && sps[i].Colour == "" && sps[i].Group != GroupQ {
			comp = append(comp, i)
		}
	}
	// Earliest known onset among the competitors, and the tiers close to it.
	var earliest *time.Time
	for _, i := range comp {
		if t := sps[i].OnsetAt; t != nil && (earliest == nil || t.Before(*earliest)) {
			earliest = t
		}
	}
	nearTiers := map[string]bool{}
	if earliest != nil {
		for _, i := range comp {
			if t := sps[i].OnsetAt; t != nil && t.Sub(*earliest) < sep {
				nearTiers[sps[i].Tier] = true
			}
		}
	}
	for _, i := range comp {
		sp := &sps[i]
		switch {
		case earliest != nil && sp.OnsetAt != nil && sp.OnsetAt.Sub(*earliest) < sep && len(nearTiers) == 1:
			sp.Colour = Red
			sp.Why += " Its tier moved first."
		case earliest != nil && sp.OnsetAt != nil && sp.OnsetAt.Sub(*earliest) < sep:
			sp.Colour = Orange
			sp.Why += " Its tier moved at about the same time as another, so they cannot be told apart."
		case earliest == nil && len(comp) == 1:
			sp.Colour = Red
			sp.Why += " It is the only suspect the data supports."
		default:
			sp.Colour = Orange
			if earliest != nil && sp.OnsetAt != nil {
				sp.Why += " Its tier moved after an earlier one."
			}
		}
	}
	// A consistent suspect left without a colour (group Q is set above) is orange.
	for i := range sps {
		if sps[i].Result == ResConsistent && sps[i].Colour == "" {
			sps[i].Colour = Orange
		}
	}
}
