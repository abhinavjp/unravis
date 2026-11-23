package keeper

import "testing"

// The keeper must read every Blackbox call through one pool. A transport per call left a
// connection open per call, and a few checks in a row filled the Blackbox's per-address limit.
func TestBlackboxReadsShareOneTransport(t *testing.T) {
	b := &Backend{}
	first, second := b.httpClient(), b.httpClient()
	if first.Transport == nil || first.Transport != second.Transport {
		t.Fatal("each Blackbox call built its own transport")
	}
}
