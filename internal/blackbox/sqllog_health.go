package blackbox

import "time"

// decodeSystemHealth reads the system_health .xel files in dir for events after
// since. Today it reports the files as not readable: the .xel binary format has no
// public specification and no sample file was available to check a reader against,
// so a guessed parser would invent numbers. The group turns this answer into
// HealthUnavailable ("not available"), once per run, and never falls back to a SQL
// right (decided 23 Nov 2026). The aggregation and the cursor around this seam are
// real and tested with a fake reader; only this function is missing, and it is the
// one place to fill once a real system_health_*.xel file has been seen.
func decodeSystemHealth(dir string, since time.Time) ([]rawHealth, error) {
	return nil, errXELUnsupported
}
