package keeper

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"api-audit-explorer/internal/audit"
)

// The collection hints and the duplicate marker survive the trip through the keeper pipe.
func TestSourceAddFailuresCrossTheKeeperWire(t *testing.T) {
	sent := &audit.ConnectionError{Stage: "source", Code: "collection_missing", Message: "no such collection", Collections: []string{"SystemApiAuditLog", "audit_old"}}
	got := native(classify(sent), kindMongo)
	var ce *audit.ConnectionError
	if !errors.As(got, &ce) || ce.Code != "collection_missing" || ce.Stage != "source" || !reflect.DeepEqual(ce.Collections, sent.Collections) {
		t.Fatalf("got %#v", got)
	}

	dup := fmt.Errorf("%w: source profile already exists: %w", audit.ErrInvalidInput, audit.ErrDuplicate)
	var we *WireError
	if !errors.As(auditWireError(dup), &we) {
		t.Fatal("the duplicate must cross as a wire error")
	}
	back := auditNative(we)
	if !errors.Is(back, audit.ErrDuplicate) || !errors.Is(back, audit.ErrInvalidInput) {
		t.Fatalf("duplicate lost on the wire: %v", back)
	}
}
