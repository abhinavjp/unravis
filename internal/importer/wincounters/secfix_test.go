package wincounters

import "testing"

// secfix L4: the default event cap is no more than the other importers allow
// (2M), so a hostile file cannot hold many GB of events in memory. The cap
// itself is exercised by TestMaxEventsLimit.
func TestDefaultMaxEventsIsBounded(t *testing.T) {
	if got := New().DefaultLimits().MaxEvents; got <= 0 || got > 2_000_000 {
		t.Errorf("MaxEvents default = %d, want 1..2000000", got)
	}
}
