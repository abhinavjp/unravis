package keeper

import (
	"net/http"
	"testing"
)

// The keeper must read every Blackbox call through one pool. A transport per call left a
// connection open per call, and a few checks in a row filled the Blackbox's per-address limit.
func TestBlackboxReadsShareOneTransport(t *testing.T) {
	b := &Backend{}
	first, second := b.httpClient(), b.httpClient()
	if first.Transport == nil || first.Transport != second.Transport {
		t.Fatal("each Blackbox call built its own transport")
	}
	// The read key travels in a header: it must never go through a proxy from the environment.
	if tr, ok := first.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatal("the keeper's Blackbox transport may use a proxy")
	}
}
