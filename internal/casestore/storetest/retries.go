package storetest

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// retriesContract: the needs-another-try list is stored in the case, replaced as
// a whole, kept apart by kind (C19), writable on a Closed case, and bounded.
func retriesContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("retries")
	r, ok := c.(casestore.RetryStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.RetryStore", c)
	}
	if got, err := r.Retries(e.ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("fresh list = %v %v, want empty non-nil", got, err)
	}
	in := []casestore.RetryItem{
		{Kind: casestore.RetryAudit, Key: "src-1", Label: "Orders database", Reason: "unreachable"},
		{Kind: casestore.RetryImport, Key: "file-1", Label: "orders.csv", Reason: "import-failed", Payload: []byte(` {"fileName":"orders.csv"} `)},
		{Kind: casestore.RetryAudit, Key: "SRC-1", Label: "dup", Reason: "unreachable"}, // same pair, any case: dropped
	}
	got, err := r.ReplaceRetries(e.ctx, in)
	if err != nil || len(got) != 2 {
		t.Fatalf("Replace: %v %v", got, err)
	}
	if got[0].ID == 0 || got[0].ID == got[1].ID || got[0].Created.IsZero() {
		t.Errorf("ids and times not assigned: %+v", got)
	}
	if got[0].Kind != casestore.RetryAudit || got[1].Kind != casestore.RetryImport {
		t.Errorf("kinds were merged: %+v", got)
	}
	if string(got[1].Payload) != `{"fileName":"orders.csv"}` || got[0].Payload != nil {
		t.Errorf("payload: %q / %q", got[1].Payload, got[0].Payload)
	}
	if again, _ := r.Retries(e.ctx); len(again) != 2 || again[0].ID != got[0].ID {
		t.Errorf("Retries differs from what Replace returned: %+v", again)
	}
	// A repeated wizard run replaces the list: the second run says what is still undone.
	got, err = r.ReplaceRetries(e.ctx, in[1:2])
	if err != nil || len(got) != 1 || got[0].Kind != casestore.RetryImport {
		t.Fatalf("second Replace: %v %v", got, err)
	}
	// Written on a Closed case: allowed (the case may have been closed mid-create).
	if err := e.store.CloseCase(e.ctx, c.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReplaceRetries(e.ctx, []casestore.RetryItem{{Kind: casestore.RetryTag, Key: "k", Label: "A tag", Reason: "case-closed"}}); err != nil {
		t.Fatalf("Replace on a closed case: %v", err)
	}
	list, _ := r.Retries(e.ctx)
	if len(list) != 1 {
		t.Fatalf("list on a closed case: %+v", list)
	}
	if err := r.RemoveRetry(e.ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveRetry(e.ctx, 9999); err != nil {
		t.Errorf("removing an item that is not there: %v", err)
	}
	if list, _ = r.Retries(e.ctx); len(list) != 0 {
		t.Errorf("list after remove: %+v", list)
	}
	// Bounds.
	bad := []casestore.RetryItem{
		{Kind: "other", Key: "k", Label: "l", Reason: "r"},
		{Kind: casestore.RetryTag, Key: "k", Label: "", Reason: "r"},
		{Kind: casestore.RetryTag, Key: "", Label: "l", Reason: "r"},
		{Kind: casestore.RetryTag, Key: "k", Label: "l", Reason: "Not Valid"},
		{Kind: casestore.RetryTag, Key: "k", Label: "l", Reason: "r", Payload: []byte("not json")},
		{Kind: casestore.RetryTag, Key: "k", Label: "l", Reason: "r", Payload: []byte(`["array"]`)},
		{Kind: casestore.RetryTag, Key: "k", Label: "l", Reason: "r", Payload: []byte(`{"a":"` + strings.Repeat("x", casestore.MaxRetryPayload) + `"}`)},
	}
	for i, b := range bad {
		if _, err := r.ReplaceRetries(e.ctx, []casestore.RetryItem{b}); !errors.Is(err, casestore.ErrBadRetry) {
			t.Errorf("bad item %d: %v, want ErrBadRetry", i, err)
		}
	}
	many := make([]casestore.RetryItem, casestore.MaxRetries+1)
	for i := range many {
		many[i] = casestore.RetryItem{Kind: casestore.RetryImport, Key: strings.Repeat("k", 5) + string(rune('a'+i%26)) + strings.Repeat("z", i/26), Label: "l", Reason: "r"}
	}
	if _, err := r.ReplaceRetries(e.ctx, many); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("too many: %v", err)
	}
	// A refused Replace leaves the old list alone.
	if _, err := r.ReplaceRetries(e.ctx, in[:1]); err != nil {
		t.Fatal(err)
	}
	_, _ = r.ReplaceRetries(e.ctx, bad[:1])
	if list, _ = r.Retries(e.ctx); len(list) != 1 {
		t.Errorf("a refused Replace changed the list: %+v", list)
	}
}
