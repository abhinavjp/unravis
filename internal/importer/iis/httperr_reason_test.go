package iis

import "testing"

// TestKnownHTTPERRReason checks the allowlist against every canonical value
// the importer itself can produce, plus "other", and a handful of values it
// never produces (including a near-miss on casing and a trailing space).
func TestKnownHTTPERRReason(t *testing.T) {
	for _, canon := range httperrReasons {
		if !KnownHTTPERRReason(canon) {
			t.Errorf("KnownHTTPERRReason(%q) = false, want true", canon)
		}
	}
	if !KnownHTTPERRReason("other") {
		t.Error(`KnownHTTPERRReason("other") = false, want true`)
	}
	for _, s := range []string{"", "connection_dropped", "DROP TABLE", "Connection_Dropped "} {
		if KnownHTTPERRReason(s) {
			t.Errorf("KnownHTTPERRReason(%q) = true, want false", s)
		}
	}
}
