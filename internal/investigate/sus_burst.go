package investigate

import "fmt"

// iisRowsIn counts IIS rows whose end falls in w.
func iisRowsIn(in Input, w Window) int {
	n := 0
	for _, r := range in.IIS {
		if w.Contains(r.End) {
			n++
		}
	}
	return n
}

// windowMissing says whether the incident window (and, with needRef, the
// comparison window) is not a readable period. Rows counted in a window that
// cannot be read would read as a quiet source, never as missing data.
func windowMissing(in Input, needRef bool) (Signal, bool) {
	if !in.Incident.Valid() {
		return missing("window", "The incident window is not a readable period, so rows cannot be counted in it."), true
	}
	if needRef && !in.Reference.Valid() {
		return missing("window", "The comparison window is not a readable period, so a rise cannot be read."), true
	}
	return Signal{}, false
}

// httpErrBurst reads HTTPERR rows whose reason is one of reasons. A burst is at
// least BurstMin5 rows in some 5 minutes of the incident, or, when the incident
// holds at least BurstMinRequests IIS rows, at least BurstShare of them. With
// rise, the incident rate must also be at least BurstRise times the reference
// rate (or the reference must hold none). what names the rows in the reason.
func httpErrBurst(in Input, th Thresholds, rise bool, what string, reasons ...string) (Signal, BurstCount) {
	if !in.HTTPErrPresent {
		return missing("httperr", "No HTTPERR log was imported for these windows."), BurstCount{}
	}
	if s, bad := windowMissing(in, rise); bad {
		return s, BurstCount{}
	}
	b := CountBurst(httpErrTimes(in, reasons...), in.Incident, in.Reference)
	n := iisRowsIn(in, in.Incident)
	burst := b.Peak5 >= th.BurstMin5 || (n >= th.BurstMinRequests && float64(b.Inc) >= th.BurstShare*float64(n))
	rose := !rise || b.RefRate() == 0 || b.IncRate() >= th.BurstRise*b.RefRate()
	switch {
	case !burst:
		return sig(No, fmt.Sprintf("%s: %d in the incident, under the burst mark.", what, b.Inc)), b
	case !rose:
		return sig(No, fmt.Sprintf("%s: %d in the incident, no higher than the comparison period.", what, b.Inc)), b
	}
	return sig(Yes, fmt.Sprintf("%s: %d in the incident, at most %d in 5 minutes.", what, b.Inc, b.Peak5)), b
}
