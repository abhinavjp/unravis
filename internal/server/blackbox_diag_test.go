package server

import (
	"net/http"
	"strings"
	"testing"
)

// A Blackbox that has no diagnostics log (an older one, or the simulated world)
// still answers the Diagnostics view, in words, with the state and a text to copy.
func TestBlackboxDiagnosticsAnswersWithoutALog(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	res := b.e.get("/api/blackbox/" + it.ID + "/diagnostics")
	if res.Code != http.StatusOK {
		t.Fatalf("diagnostics = %d %s", res.Code, res.Body)
	}
	var out bbDiagResponse
	decodeBody(t, res, &out)
	if out.Headline == "" || out.Label == "" || !strings.Contains(out.Text, "Likely cause:") {
		t.Fatalf("answer = %+v", out)
	}
	if res := b.e.get("/api/blackbox/bb-000000000000000000000099/diagnostics"); res.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", res.Code)
	}
}
