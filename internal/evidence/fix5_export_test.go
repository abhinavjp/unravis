package evidence

// Accessors for the external test package (fix5_drift_test.go). Test files of
// package evidence are compiled with evidence_test, so these exist only under
// go test.

// ExportPolicyKeys returns, per source, the attribute names the case export
// policy lists.
func ExportPolicyKeys() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(exportPolicy))
	for src, pol := range exportPolicy {
		m := make(map[string]bool, len(pol))
		for k := range pol {
			m[k] = true
		}
		out[src] = m
	}
	return out
}

// ExportDroppedKeys returns, per source, the attribute names the case export
// leaves out on purpose.
func ExportDroppedKeys() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(exportDropped))
	for src, d := range exportDropped {
		m := make(map[string]bool, len(d))
		for k := range d {
			m[k] = true
		}
		out[src] = m
	}
	return out
}
