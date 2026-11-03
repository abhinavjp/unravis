package investigate

import (
	"fmt"
	"math"
)

// sigPayloadUp compares the P95 of PayloadRecordTime between the windows (S8).
// Requests are placed by their start. It needs GroupSizeP95 values on each side
// and both change marks (ChangeMinMs and ChangeMinPercent) to read yes.
func sigPayloadUp(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	if in.Audit != AuditPresent {
		return missing("audit", "There is no audit data for these windows.")
	}
	var inc, ref []float64
	for _, r := range in.Requests {
		// A value that is not a finite, non-negative time is a bad reading, not a
		// quiet one: leaving it out keeps it from reading as a rise or a No.
		if r.P == nil || math.IsNaN(*r.P) || math.IsInf(*r.P, 0) || *r.P < 0 {
			continue
		}
		switch {
		case in.Incident.Contains(r.Start):
			inc = append(inc, *r.P)
		case in.Reference.Contains(r.Start):
			ref = append(ref, *r.P)
		}
	}
	if len(inc)+len(ref) == 0 {
		return missing("payload", "The audit holds no PayloadRecordTime for these windows.")
	}
	if len(inc) < th.GroupSizeP95 || len(ref) < th.GroupSizeP95 {
		return sig(Unknown, fmt.Sprintf("Too few PayloadRecordTime values to compare: %d in the incident, %d in the comparison period; %d are needed on each side.", len(inc), len(ref), th.GroupSizeP95))
	}
	pi := nearestRank(sortedCopy(inc), 0.95)
	pr := nearestRank(sortedCopy(ref), 0.95)
	up := pi-pr >= th.ChangeMinMs && pi >= pr*(1+th.ChangeMinPercent/100)
	if up {
		return sig(Yes, fmt.Sprintf("PayloadRecordTime P95 rose from %.0f ms to %.0f ms.", pr, pi))
	}
	return sig(No, fmt.Sprintf("PayloadRecordTime P95 went from %.0f ms to %.0f ms, under the change mark.", pr, pi))
}
