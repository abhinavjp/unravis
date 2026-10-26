package calltrace

import (
	"fmt"
	"math"
)

// Thresholds for the checks. They are fixed numbers, shown in no sentence.
const (
	auditShareHigh = 0.5
	auditMinMs     = 1000.0
	auditShareLow  = 0.1
	apiShareHigh   = 0.8
	apiShareLow    = 0.5
)

const checkLabel = "Symptom"

// Fixed details for a lane that cannot be judged.
const (
	detailIncomplete = "The read for this call hit its limit, so events may be missing."
	detailOutOfRange = "The files for this source do not cover this time."
	detailUnchecked  = "A file for this source has no checked time zone, so it was not plotted."
)

// laneNeeds is the missing-file text per lane key.
var laneNeeds = map[string]string{
	LaneSQL:   "a SQL Server log or export",
	LaneMongo: "a MongoDB log or export",
	LaneRedis: "a Redis log or export",
	LaneIIS:   "an IIS log",
}

// laneNames is the plain name per lane key, used by the compare checks.
var laneNames = map[string]string{
	LaneSQL:   "SQL Server",
	LaneMongo: "MongoDB",
	LaneRedis: "Redis",
	LaneIIS:   "IIS",
}

// partNames is the plain name per part key.
var partNames = map[string]string{
	PartAudit: "audit write",
	PartAPI:   "API work",
	PartOther: "other time",
}

func partMs(c Call, key string) (float64, bool) {
	for _, p := range c.Parts {
		if p.Key == key {
			return p.Ms, true
		}
	}
	return 0, false
}

func knownDuration(c Call) (float64, bool) {
	if c.DurationMs != nil && *c.DurationMs > 0 {
		return *c.DurationMs, true
	}
	return 0, false
}

func pct(share float64) int { return int(math.Round(share * 100)) }

func ms(v float64) int { return int(math.Round(v)) }

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// RunChecks returns the checks for one call, in a fixed order, from its parts and its lanes.
func RunChecks(c Call, lanes []Lane) []Check {
	dur, durOK := knownDuration(c)
	usable := c.End != nil && !c.ClockAnomaly && durOK
	ids := []string{c.ID}
	mk := func(id, title, status, detail string) Check {
		return Check{ID: id, Title: title, Status: status, Detail: detail, CallIDs: ids, Label: checkLabel}
	}
	out := make([]Check, 0, 2+len(lanes))

	// 1. The audit write.
	audit, auditOK := partMs(c, PartAudit)
	switch share := audit / dur; {
	case !auditOK || !durOK:
		out = append(out, mk("audit-write-slow", "The audit write was slow", StatusNotEnoughData,
			"The log has no audit time for this call."))
	case audit >= auditMinMs && share >= auditShareHigh:
		out = append(out, mk("audit-write-slow", "The audit write was slow", StatusConsistent,
			fmt.Sprintf("The audit write took %d ms, %d%% of the %d ms call.", ms(audit), pct(share), ms(dur))))
	case share < auditShareLow:
		out = append(out, mk("audit-write-slow", "The audit write was slow", StatusContradicted,
			fmt.Sprintf("The audit write took %d ms, %d%% of the %d ms call.", ms(audit), pct(share), ms(dur))))
	default:
		out = append(out, mk("audit-write-slow", "The audit write was slow", StatusNotEnoughData,
			"The audit write took part of the time; it is neither most of it nor a small part."))
	}

	// 2. The API's own work.
	api, apiOK := partMs(c, PartAPI)
	const apiTitle = "Most of the time was the API's own work"
	switch share := api / dur; {
	case !apiOK || !durOK:
		out = append(out, mk("api-work-dominates", apiTitle, StatusNotEnoughData,
			"The log has no API time for this call."))
	case share >= apiShareHigh:
		out = append(out, mk("api-work-dominates", apiTitle, StatusConsistent,
			fmt.Sprintf("The API work took %d ms, %d%% of the %d ms call.", ms(api), pct(share), ms(dur))))
	case share < apiShareLow:
		out = append(out, mk("api-work-dominates", apiTitle, StatusContradicted,
			fmt.Sprintf("The API work took %d ms, %d%% of the %d ms call.", ms(api), pct(share), ms(dur))))
	default:
		out = append(out, mk("api-work-dominates", apiTitle, StatusNotEnoughData,
			"The API work took part of the time; it is neither most of it nor a small part."))
	}

	// 3. One check per main lane present in lanes.
	for _, l := range lanes {
		needs, main := laneNeeds[l.Key]
		if !main {
			continue
		}
		id := l.Key + "-during-call"
		title := l.Label + " was busy during this call"
		if !usable {
			out = append(out, mk(id, title, StatusNotEnoughData,
				"The log has no usable finish time for this call, so nothing was compared."))
			continue
		}
		switch l.State {
		case StateEvents:
			out = append(out, mk(id, title, StatusConsistent, eventsDetail(l)))
		case StateNoneSeen:
			out = append(out, mk(id, title, StatusContradicted,
				"The files in this case cover the call, and no event from this source overlapped it."))
		case StateNoData:
			ck := mk(id, title, StatusNotTestable, "This case has no file for this source.")
			ck.Needs = needs
			out = append(out, ck)
		case StateNoCase:
			ck := mk(id, title, StatusNotTestable, "No case is open, so there are no imported files to read.")
			ck.Needs = needs
			out = append(out, ck)
		case StateIncomplete:
			out = append(out, mk(id, title, StatusNotEnoughData, detailIncomplete))
		case StateOutOfRange:
			out = append(out, mk(id, title, StatusNotEnoughData, detailOutOfRange))
		default: // StateClockUnchecked and anything unknown
			out = append(out, mk(id, title, StatusNotEnoughData, detailUnchecked))
		}
	}
	return out
}

func eventsDetail(l Lane) string {
	n := len(l.Events) + l.Hidden
	s := fmt.Sprintf("%s from this source overlapped the call", plural(n, "event"))
	longest := -1.0
	for _, e := range l.Events {
		if e.DurationMs != nil && *e.DurationMs > longest {
			longest = *e.DurationMs
		}
	}
	if longest >= 0 {
		s += fmt.Sprintf("; the longest ran %d ms", ms(longest))
	}
	return s + "."
}

// CompareChecks returns checks that compare 2 or 3 traces. nil for fewer than 2.
func CompareChecks(traces []Trace) []Check {
	if len(traces) < 2 {
		return nil
	}
	ids := make([]string, len(traces))
	for i, t := range traces {
		ids[i] = t.Call.ID
	}
	mk := func(id, title, status, detail string) Check {
		return Check{ID: id, Title: title, Status: status, Detail: detail, CallIDs: ids, Label: checkLabel}
	}
	out := make([]Check, 0, 6)

	// 1. The largest part in each call.
	const mainTitle = "The same part took the most time in every call"
	tops := make([]string, len(traces))
	missing := false
	for i, t := range traces {
		best, bestMs := "", -1.0
		for _, key := range []string{PartAudit, PartAPI, PartOther} {
			if v, ok := partMs(t.Call, key); ok && v > bestMs {
				best, bestMs = key, v
			}
		}
		if best == "" {
			missing = true
		}
		tops[i] = best
	}
	switch {
	case missing:
		out = append(out, mk("same-main-part", mainTitle, StatusNotEnoughData,
			"At least one call has no audit, API or other time in the log."))
	default:
		same := true
		for _, k := range tops[1:] {
			if k != tops[0] {
				same = false
			}
		}
		if same {
			out = append(out, mk("same-main-part", mainTitle, StatusConsistent,
				fmt.Sprintf("The %s took the most time in all %d calls.", partNames[tops[0]], len(traces))))
		} else {
			detail := "The largest part differed between calls:"
			for i, k := range tops {
				sep := ","
				if i == 0 {
					sep = ""
				}
				detail += fmt.Sprintf("%s call %d, %s", sep, i+1, partNames[k])
			}
			out = append(out, mk("same-main-part", mainTitle, StatusContradicted, detail+"."))
		}
	}

	// 2. Each main lane.
	for _, key := range []string{LaneSQL, LaneMongo, LaneRedis, LaneIIS} {
		id := key + "-in-every-call"
		title := laneNames[key] + " was busy in every call"
		var noFile, unchecked, incomplete, outOfRange bool
		withCount := 0
		for _, t := range traces {
			state := StateNoData
			for _, l := range t.Lanes {
				if l.Key == key {
					state = l.State
				}
			}
			switch state {
			case StateNoData, StateNoCase:
				noFile = true
			case StateEvents:
				withCount++
			case StateNoneSeen:
			case StateIncomplete:
				incomplete = true
			case StateOutOfRange:
				outOfRange = true
			default:
				unchecked = true
			}
		}
		withEvents := withCount == len(traces)
		switch {
		case noFile:
			ck := mk(id, title, StatusNotTestable, "At least one call has no file for this source.")
			ck.Needs = laneNeeds[key]
			out = append(out, ck)
		case unchecked:
			out = append(out, mk(id, title, StatusNotEnoughData, detailUnchecked))
		case incomplete:
			out = append(out, mk(id, title, StatusNotEnoughData, "The read for at least one call hit its limit, so events may be missing."))
		case outOfRange:
			out = append(out, mk(id, title, StatusNotEnoughData, detailOutOfRange))
		case withEvents:
			out = append(out, mk(id, title, StatusConsistent,
				fmt.Sprintf("Events from this source overlapped all %d calls.", len(traces))))
		default:
			out = append(out, mk(id, title, StatusContradicted,
				fmt.Sprintf("%d of %d calls had an event from this source; the rest had none.", withCount, len(traces))))
		}
	}

	// 3. Spread of the durations.
	const spreadTitle = "The calls took about the same time"
	lo, hi, allKnown := math.Inf(1), 0.0, true
	for _, t := range traces {
		d, ok := knownDuration(t.Call)
		if !ok {
			allKnown = false
			break
		}
		lo, hi = math.Min(lo, d), math.Max(hi, d)
	}
	if !allKnown {
		out = append(out, mk("duration-spread", spreadTitle, StatusNotEnoughData,
			"At least one call has no duration in the log."))
		return out
	}
	ratio := hi / lo
	detail := fmt.Sprintf("The slowest call took %.1f times the fastest.", ratio)
	switch {
	case ratio <= 1.5:
		out = append(out, mk("duration-spread", spreadTitle, StatusConsistent, detail))
	case ratio >= 3:
		out = append(out, mk("duration-spread", spreadTitle, StatusContradicted, detail))
	default:
		out = append(out, mk("duration-spread", spreadTitle, StatusNotEnoughData, detail))
	}
	return out
}
