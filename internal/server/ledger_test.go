package server

import (
	"api-audit-explorer/internal/ledger"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeLedger is an in-memory ledger.Store so these tests do not depend on the
// real ledger implementation.
type fakeLedger struct {
	items   []ledger.Hypothesis
	next    int
	failing error
}

func (f *fakeLedger) find(id string) int {
	for i := range f.items {
		if f.items[i].ID == id {
			return i
		}
	}
	return -1
}

func (f *fakeLedger) List() ([]ledger.Hypothesis, error) {
	if f.failing != nil {
		return nil, f.failing
	}
	return f.items, nil
}

func (f *fakeLedger) Create(text string) (ledger.Hypothesis, error) {
	if f.failing != nil {
		return ledger.Hypothesis{}, f.failing
	}
	if strings.TrimSpace(text) == "" {
		return ledger.Hypothesis{}, ledger.ErrInvalid
	}
	f.next++
	h := ledger.Hypothesis{ID: "h" + strconv.Itoa(f.next), Text: text, State: ledger.StateOpen, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}
	f.items = append(f.items, h)
	return h, nil
}

func (f *fakeLedger) Update(id string, in ledger.Input) (ledger.Hypothesis, error) {
	i := f.find(id)
	if i < 0 {
		return ledger.Hypothesis{}, ledger.ErrNotFound
	}
	if in.State == ledger.StateStrong {
		return ledger.Hypothesis{}, &ledger.GuardError{Reasons: []string{"Support is temporal overlap alone.", "No alternative explanation is named."}}
	}
	f.items[i].Text, f.items[i].State, f.items[i].Notes = in.Text, in.State, in.Notes
	return f.items[i], nil
}

func (f *fakeLedger) Reuse(id string) (ledger.Hypothesis, error) {
	i := f.find(id)
	if i < 0 {
		return ledger.Hypothesis{}, ledger.ErrNotFound
	}
	return f.Create(f.items[i].Text)
}

func (f *fakeLedger) Delete(id string) error {
	i := f.find(id)
	if i < 0 {
		return ledger.ErrNotFound
	}
	f.items = append(f.items[:i], f.items[i+1:]...)
	return nil
}

func newLedgerHandler(store ledger.Store) http.Handler {
	return New(&fakeRepository{}, nil, testToken, false, WithLedger(store))
}

func TestLedgerRoutesRequireSessionToken(t *testing.T) {
	h := newLedgerHandler(&fakeLedger{})
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/ledger", ""},
		{http.MethodPost, "/api/ledger", `{"text":"x"}`},
		{http.MethodPut, "/api/ledger/h1", `{}`},
		{http.MethodPost, "/api/ledger/h1/reuse", ""},
		{http.MethodDelete, "/api/ledger/h1", ""},
		{http.MethodPost, "/api/patterns", `{}`},
	} {
		if r := request(h, c.method, c.path, c.body, false); r.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token = %d", c.method, c.path, r.Code)
		}
	}
}

func TestLedgerListIsEmptyArrayNotNull(t *testing.T) {
	h := newLedgerHandler(&fakeLedger{})
	r := request(h, http.MethodGet, "/api/ledger", "", true)
	if r.Code != http.StatusOK || strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("list = %d %q", r.Code, r.Body.String())
	}
}

func TestLedgerCRUD(t *testing.T) {
	store := &fakeLedger{}
	h := newLedgerHandler(store)

	r := request(h, http.MethodPost, "/api/ledger", `{"text":"Audit insert stalls the request"}`, true)
	if r.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", r.Code, r.Body.String())
	}
	var created ledger.Hypothesis
	if err := json.Unmarshal(r.Body.Bytes(), &created); err != nil || created.ID == "" || created.State != ledger.StateOpen {
		t.Fatalf("create body: %v %+v", err, created)
	}

	r = request(h, http.MethodPut, "/api/ledger/"+created.ID, `{"text":"edited","state":"supported_by_correlation","evidenceIds":[],"notes":"n","facts":{}}`, true)
	var updated ledger.Hypothesis
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &updated) != nil || updated.Text != "edited" || updated.State != ledger.StateSupported {
		t.Fatalf("update = %d %s", r.Code, r.Body.String())
	}

	r = request(h, http.MethodPost, "/api/ledger/"+created.ID+"/reuse", "", true)
	var reused ledger.Hypothesis
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &reused) != nil || reused.ID == "" || reused.ID == created.ID || reused.State != ledger.StateOpen {
		t.Fatalf("reuse = %d %s", r.Code, r.Body.String())
	}

	r = request(h, http.MethodGet, "/api/ledger", "", true)
	var list []ledger.Hypothesis
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &list) != nil || len(list) != 2 {
		t.Fatalf("list = %d %s", r.Code, r.Body.String())
	}

	if r = request(h, http.MethodDelete, "/api/ledger/"+created.ID, "", true); r.Code != http.StatusNoContent || r.Body.Len() != 0 {
		t.Fatalf("delete = %d %s", r.Code, r.Body.String())
	}
	if len(store.items) != 1 {
		t.Fatalf("delete left %d items", len(store.items))
	}
}

func TestLedgerGuardBlockedIs422WithReasons(t *testing.T) {
	store := &fakeLedger{}
	h := newLedgerHandler(store)
	created, _ := store.Create("h")
	r := request(h, http.MethodPut, "/api/ledger/"+created.ID, `{"text":"h","state":"supported_by_strong_correlation","evidenceIds":[],"notes":"","facts":{}}`, true)
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s", r.Code, r.Body.String())
	}
	var body struct {
		Error   string   `json:"error"`
		Code    string   `json:"code"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "guard_blocked" || body.Error == "" || len(body.Reasons) != 2 || body.Reasons[0] != "Support is temporal overlap alone." {
		t.Fatalf("422 body = %+v", body)
	}
}

func TestLedgerErrorMapping(t *testing.T) {
	store := &fakeLedger{}
	h := newLedgerHandler(store)
	if r := request(h, http.MethodPut, "/api/ledger/missing", `{"text":"x","state":"open","evidenceIds":[],"notes":"","facts":{}}`, true); r.Code != http.StatusNotFound {
		t.Fatalf("update missing = %d", r.Code)
	}
	if r := request(h, http.MethodDelete, "/api/ledger/missing", "", true); r.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d", r.Code)
	}
	if r := request(h, http.MethodPost, "/api/ledger/missing/reuse", "", true); r.Code != http.StatusNotFound {
		t.Fatalf("reuse missing = %d", r.Code)
	}
	r := request(h, http.MethodPost, "/api/ledger", `{"text":"  "}`, true)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), ledger.ErrInvalid.Error()) {
		t.Fatalf("invalid create = %d %s", r.Code, r.Body.String())
	}
	store.failing = errors.New("disk path secret-location exploded")
	r = request(h, http.MethodGet, "/api/ledger", "", true)
	if r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "secret") {
		t.Fatalf("internal error = %d %s", r.Code, r.Body.String())
	}
}

func TestLedgerRejectsBadRequests(t *testing.T) {
	h := newLedgerHandler(&fakeLedger{})
	if r := request(h, http.MethodPost, "/api/ledger", `{"text":"x","extra":1}`, true); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", r.Code)
	}
	if r := request(h, http.MethodPatch, "/api/ledger", "", true); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("patch list = %d", r.Code)
	}
	if r := request(h, http.MethodGet, "/api/ledger/h1", "", true); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get item = %d", r.Code)
	}
	if r := request(h, http.MethodPost, "/api/ledger/h1/other", "", true); r.Code != http.StatusNotFound {
		t.Fatalf("unknown action = %d", r.Code)
	}
}

func TestLedgerUnavailableWithoutStore(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/ledger", ""},
		{http.MethodPost, "/api/ledger", `{"text":"x"}`},
		{http.MethodPut, "/api/ledger/h1", `{}`},
		{http.MethodPost, "/api/ledger/h1/reuse", ""},
		{http.MethodDelete, "/api/ledger/h1", ""},
	} {
		r := request(h, c.method, c.path, c.body, true)
		if r.Code != http.StatusServiceUnavailable || !strings.Contains(r.Body.String(), "The ledger is unavailable.") {
			t.Fatalf("%s %s = %d %s", c.method, c.path, r.Code, r.Body.String())
		}
	}
}
