package redis

// MetricInfo is one INFO figure the timeline keeps, as the INFO importer reads it. The Blackbox source importer uses the
// same list, so a recording of the Blackbox and an INFO file read by the owner's script give the same metric, unit and
// scale.
type MetricInfo struct {
	Key  string  // the INFO field, also the "metric" attribute
	Unit string  // the unit of the event
	Div  float64 // the raw value is divided by this
	CPU  bool    // a counter of CPU seconds: the event is the rate between two samples, in percent of one core
	Max  float64 // a raw value above this is not a valid reading
}

// InfoMetrics returns the INFO allowlist in emission order.
func InfoMetrics() []MetricInfo {
	out := make([]MetricInfo, len(metricSpecs))
	for i, s := range metricSpecs {
		out[i] = MetricInfo{Key: s.key, Unit: s.unit, Div: s.div, CPU: s.cpu, Max: s.max}
	}
	return out
}

// CanonicalRole maps an INFO role word ("slave" and "replica" both read as "replica") to the export role, or "".
func CanonicalRole(v string) string { return roleOf(v) }

// CanonicalLatencyEvent returns the table spelling of a LATENCY event name, or Other when the name is not one of the
// documented events. The result is always a listed value, never text from a file.
func CanonicalLatencyEvent(name string) string {
	if !reLatencyName.MatchString(name) {
		return Other
	}
	return canonicalLatencyEvent(name)
}
