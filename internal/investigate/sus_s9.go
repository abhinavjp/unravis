package investigate

import (
	"fmt"
	"time"
)

// filterCodes are the IIS request-filtering rejections: status.sub-status.
// Status 431 (header fields too large) counts whatever its sub-status.
var filterCodes = map[string]bool{
	"404.5": true, "404.6": true, "404.7": true, "404.8": true,
	"404.11": true, "404.12": true, "404.14": true, "404.15": true,
	"404.18": true, "404.19": true, "413.1": true,
}

// isFilterReject says whether one IIS row is a request-filtering rejection.
func isFilterReject(r IISRow) bool {
	return r.Status == "431" || filterCodes[r.Status+"."+r.SubStatus]
}

// sigFilterReject counts request-filtering rejections in the IIS rows (S9).
func sigFilterReject(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	if !in.IISPresent {
		return missing("iis", "There are no IIS log rows for these windows.")
	}
	if !in.IISSubStatus {
		return missing("substatus", "The IIS rows carry no sub-status, so request-filtering rejections cannot be told from other 404s.")
	}
	if s, bad := windowMissing(in, true); bad {
		return s
	}
	var ts []time.Time
	for _, r := range in.IIS {
		if isFilterReject(r) {
			ts = append(ts, r.End)
		}
	}
	b := CountBurst(ts, in.Incident, in.Reference)
	rose := b.RefRate() == 0 || b.IncRate() >= th.FilterRise*b.RefRate()
	switch {
	case b.Inc >= th.FilterMin && rose:
		return sig(Yes, fmt.Sprintf("%d request-filtering rejections in the incident, at most %d in 5 minutes.", b.Inc, b.Peak5))
	case iisRowsIn(in, in.Incident) >= th.GroupSizeP95:
		return sig(No, fmt.Sprintf("%d request-filtering rejections in the incident, no rise over the comparison period.", b.Inc))
	}
	return sig(Unknown, "Too few IIS rows in the incident to read request-filtering rejections.")
}
