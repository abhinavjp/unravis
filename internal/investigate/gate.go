package investigate

// spreadOK says whether the reference events are spread over the covered
// reference minutes: a reference that is one burst cannot stand for normal.
// counts is events per covered reference minute, zeros included.
func spreadOK(counts []int, th Thresholds) bool {
	if len(counts) == 0 {
		return false
	}
	total, withEvent, max := 0, 0, 0
	for _, c := range counts {
		total += c
		if c > 0 {
			withEvent++
		}
		if c > max {
			max = c
		}
	}
	n := len(counts)
	mean := float64(total) / float64(n)
	// At a trickle, empty minutes are expected: the rule is skipped.
	if mean < th.GateSpreadSkipBelowPerMin-floatEps {
		return true
	}
	if float64(withEvent)/float64(n) >= th.GateSpreadMinShare-floatEps {
		return true
	}
	if total > 0 && float64(max)/float64(total) <= th.GateSpreadMaxMinuteShare+floatEps {
		return true
	}
	return false
}

// Gate says whether the reference holds enough events, spread over enough
// minutes, to compare against. It is computed once per run and never serialized.
type Gate struct {
	RefEvents int
	IncEvents int
	RateOK    bool // rate comparison allowed
	P95OK     bool // reference P95 comparison allowed
	SpreadOK  bool
}

func evaluateGate(refN, incN int, refMinuteCounts []int, th Thresholds) Gate {
	g := Gate{RefEvents: refN, IncEvents: incN}
	g.SpreadOK = spreadOK(refMinuteCounts, th)
	g.RateOK = refN >= th.GateRefMinEvents && incN >= th.GateIncMinEvents && g.SpreadOK
	g.P95OK = g.RateOK && refN >= th.GateRefMinEventsP95
	return g
}
