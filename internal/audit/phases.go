package audit

import "math"

// phaseDelta is each phase's mean change, current minus reference. A phase is
// nil when either window has no samples for it, and the result is nil when no
// phase can be compared.
func phaseDelta(current, reference Stats) *PhaseMeans {
	if current.Phases == nil || reference.Phases == nil {
		return nil
	}
	delta := &PhaseMeans{
		AuditMs:   meanDifference(current.Phases.AuditMs, reference.Phases.AuditMs),
		ApiMs:     meanDifference(current.Phases.ApiMs, reference.Phases.ApiMs),
		PayloadMs: meanDifference(current.Phases.PayloadMs, reference.Phases.PayloadMs),
	}
	if delta.AuditMs == nil && delta.ApiMs == nil && delta.PayloadMs == nil {
		return nil
	}
	return delta
}

func meanDifference(current, reference *float64) *float64 {
	if current == nil || reference == nil {
		return nil
	}
	value := *current - *reference
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
