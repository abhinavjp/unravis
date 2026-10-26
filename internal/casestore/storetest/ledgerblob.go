package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func blobber(t *testing.T, c casestore.Case) casestore.LedgerBlobStore {
	t.Helper()
	b, ok := c.(casestore.LedgerBlobStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.LedgerBlobStore", c)
	}
	return b
}

// ledgerBlobContract is the contract of the per-case theories blob: none until
// set, replaced as a whole, copied both ways, kept per case, bounded, readable on
// a closed case but not writable, and refused on a closed handle or erased case.
func ledgerBlobContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	la, lb := blobber(t, a), blobber(t, b)
	ctx := e.ctx

	if got, err := la.LedgerBlob(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh LedgerBlob = %q %v, want none", got, err)
	}

	first := []byte(`{"version":1,"hypotheses":[]}`)
	if err := la.SetLedgerBlob(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, err := la.LedgerBlob(ctx); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("LedgerBlob = %q %v", got, err)
	}
	// The caller's slice and the returned slice are copies.
	first[0] = 'X'
	got, _ := la.LedgerBlob(ctx)
	if got[0] != '{' {
		t.Error("SetLedgerBlob kept the caller's slice")
	}
	got[0] = 'Y'
	if again, _ := la.LedgerBlob(ctx); again[0] != '{' {
		t.Error("LedgerBlob returned the stored slice, not a copy")
	}

	// Replaced as a whole, kept per case.
	second := []byte(`{"version":1,"hypotheses":[{"id":"x"}]}`)
	if err := la.SetLedgerBlob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := la.LedgerBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("replace: %q", got)
	}
	if got, err := lb.LedgerBlob(ctx); err != nil || len(got) != 0 {
		t.Errorf("blob leaked into another case: %q %v", got, err)
	}

	// Bounds: over the cap is refused and changes nothing; the cap itself is accepted.
	if err := la.SetLedgerBlob(ctx, make([]byte, casestore.MaxLedgerBlob+1)); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("over the cap: %v", err)
	}
	if got, _ := la.LedgerBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("a refused write changed the blob")
	}
	if err := lb.SetLedgerBlob(ctx, bytes.Repeat([]byte("a"), casestore.MaxLedgerBlob)); err != nil {
		t.Errorf("blob at the cap: %v", err)
	}
	if err := lb.SetLedgerBlob(ctx, nil); err != nil {
		t.Errorf("clearing the blob: %v", err)
	}
	if got, _ := lb.LedgerBlob(ctx); len(got) != 0 {
		t.Errorf("cleared blob reads %d bytes", len(got))
	}

	// A cancelled context stops the write before it changes anything.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := la.SetLedgerBlob(cctx, []byte("never")); err == nil {
		t.Error("SetLedgerBlob ran on a cancelled context")
	}
	if got, _ := la.LedgerBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("cancelled write changed the blob: %q", got)
	}

	// Another handle sees the blob; a closed handle refuses.
	again, err := e.store.Open(ctx, a.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := blobber(t, again).LedgerBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("second handle reads %q", got)
	}
	_ = again.Close()
	if _, err := blobber(t, again).LedgerBlob(ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle read: %v", err)
	}
	if err := blobber(t, again).SetLedgerBlob(ctx, first); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle write: %v", err)
	}

	// A closed case keeps the blob readable and takes no more.
	if err := e.store.CloseCase(ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if err := la.SetLedgerBlob(ctx, first); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("SetLedgerBlob on a closed case: %v", err)
	}
	if got, err := la.LedgerBlob(ctx); err != nil || !bytes.Equal(got, second) {
		t.Errorf("closed case LedgerBlob = %q %v", got, err)
	}

	// An erased case answers ErrNotFound and keeps nothing.
	if err := lb.SetLedgerBlob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Erase(ctx, b.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lb.LedgerBlob(ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased LedgerBlob: %v", err)
	}
	if err := lb.SetLedgerBlob(ctx, second); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased SetLedgerBlob: %v", err)
	}
}
