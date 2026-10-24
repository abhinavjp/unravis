package server

import (
	"api-audit-explorer/internal/ledger"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLedgerSettlingObservationRoundTripAndLimit(t *testing.T) {
	store, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := newLedgerHandler(store)
	r := request(h, http.MethodPost, "/api/ledger", `{"text":"Audit insert stalls the request"}`, true)
	var created ledger.Hypothesis
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", r.Code, r.Body.String())
	}
	put := func(obs string) (int, string) {
		b, _ := json.Marshal(map[string]any{"text": "edited", "state": "open", "evidenceIds": []string{}, "notes": "", "settlingObservation": obs, "facts": map[string]any{}})
		r := request(h, http.MethodPut, "/api/ledger/"+created.ID, string(b), true)
		return r.Code, r.Body.String()
	}
	code, body := put("  The stall is gone on the next quiet night.  ")
	var updated ledger.Hypothesis
	if code != http.StatusOK || json.Unmarshal([]byte(body), &updated) != nil || updated.SettlingObservation != "The stall is gone on the next quiet night." {
		t.Fatalf("update = %d %s", code, body)
	}
	if !strings.Contains(body, `"settlingObservation"`) {
		t.Fatalf("json key missing: %s", body)
	}
	if code, body := put(strings.Repeat("x", 501)); code != http.StatusBadRequest || !strings.Contains(body, "500") {
		t.Fatalf("over length = %d %s", code, body)
	}
	if code, _ := put("a\u0001b"); code != http.StatusBadRequest {
		t.Fatalf("control character = %d", code)
	}
	list, _ := store.List()
	if len(list) != 1 || list[0].SettlingObservation != "The stall is gone on the next quiet night." {
		t.Fatalf("rejected edits changed the entry: %+v", list)
	}
	r = request(h, http.MethodPost, "/api/ledger/"+created.ID+"/reuse", "", true)
	var reused ledger.Hypothesis
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &reused) != nil || reused.SettlingObservation != updated.SettlingObservation {
		t.Fatalf("reuse = %d %s", r.Code, r.Body.String())
	}
}
