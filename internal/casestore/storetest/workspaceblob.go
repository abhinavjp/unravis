package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func workspacer(t *testing.T, c casestore.Case) casestore.WorkspaceBlobStore {
	t.Helper()
	b, ok := c.(casestore.WorkspaceBlobStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.WorkspaceBlobStore", c)
	}
	return b
}

// workspaceBlobContract is the contract of the per-case workspace note: none
// until set, replaced as a whole, copied both ways, kept per case, bounded,
// cleared by nil or empty, readable on a closed case but not writable, and
// refused on a closed handle or erased case.
func workspaceBlobContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	wa, wb := workspacer(t, a), workspacer(t, b)
	ctx := e.ctx

	if got, err := wa.WorkspaceBlob(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh WorkspaceBlob = %q %v, want none", got, err)
	}

	first := []byte(`{"version":1,"filters":{}}`)
	if err := wa.SetWorkspaceBlob(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got, err := wa.WorkspaceBlob(ctx); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("WorkspaceBlob = %q %v", got, err)
	}
	// The caller's slice and the returned slice are copies.
	first[0] = 'X'
	got, _ := wa.WorkspaceBlob(ctx)
	if got[0] != '{' {
		t.Error("SetWorkspaceBlob kept the caller's slice")
	}
	got[0] = 'Y'
	if again, _ := wa.WorkspaceBlob(ctx); again[0] != '{' {
		t.Error("WorkspaceBlob returned the stored slice, not a copy")
	}

	// Replaced as a whole, kept per case, separate from the ledger blob.
	second := []byte(`{"version":1,"filters":{"range":"7d"}}`)
	if err := wa.SetWorkspaceBlob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := wa.WorkspaceBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("replace: %q", got)
	}
	if got, err := wb.WorkspaceBlob(ctx); err != nil || len(got) != 0 {
		t.Errorf("blob leaked into another case: %q %v", got, err)
	}
	if got, err := blobber(t, a).LedgerBlob(ctx); err != nil || len(got) != 0 {
		t.Errorf("workspace leaked into the ledger blob: %q %v", got, err)
	}

	// Bounds: over the cap is refused and changes nothing; the cap itself is accepted.
	if err := wa.SetWorkspaceBlob(ctx, make([]byte, casestore.MaxWorkspaceBlob+1)); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("over the cap: %v", err)
	}
	if got, _ := wa.WorkspaceBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("a refused write changed the blob")
	}
	if err := wb.SetWorkspaceBlob(ctx, bytes.Repeat([]byte("a"), casestore.MaxWorkspaceBlob)); err != nil {
		t.Errorf("blob at the cap: %v", err)
	}
	if err := wb.SetWorkspaceBlob(ctx, nil); err != nil {
		t.Errorf("clearing the blob: %v", err)
	}
	if got, _ := wb.WorkspaceBlob(ctx); len(got) != 0 {
		t.Errorf("cleared blob reads %d bytes", len(got))
	}
	if err := wb.SetWorkspaceBlob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := wb.SetWorkspaceBlob(ctx, []byte{}); err != nil {
		t.Errorf("clearing with an empty slice: %v", err)
	}
	if got, _ := wb.WorkspaceBlob(ctx); len(got) != 0 {
		t.Errorf("blob cleared by an empty slice reads %d bytes", len(got))
	}

	// A cancelled context stops the write before it changes anything.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := wa.SetWorkspaceBlob(cctx, []byte("never")); err == nil {
		t.Error("SetWorkspaceBlob ran on a cancelled context")
	}
	if got, _ := wa.WorkspaceBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("cancelled write changed the blob: %q", got)
	}

	// Another handle sees the blob; a closed handle refuses.
	again, err := e.store.Open(ctx, a.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := workspacer(t, again).WorkspaceBlob(ctx); !bytes.Equal(got, second) {
		t.Errorf("second handle reads %q", got)
	}
	_ = again.Close()
	if _, err := workspacer(t, again).WorkspaceBlob(ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle read: %v", err)
	}
	if err := workspacer(t, again).SetWorkspaceBlob(ctx, first); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle write: %v", err)
	}

	// A closed case keeps the blob readable and takes no more.
	if err := e.store.CloseCase(ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if err := wa.SetWorkspaceBlob(ctx, first); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("SetWorkspaceBlob on a closed case: %v", err)
	}
	if got, err := wa.WorkspaceBlob(ctx); err != nil || !bytes.Equal(got, second) {
		t.Errorf("closed case WorkspaceBlob = %q %v", got, err)
	}

	// An erased case answers ErrNotFound and keeps nothing.
	if err := wb.SetWorkspaceBlob(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Erase(ctx, b.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.WorkspaceBlob(ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased WorkspaceBlob: %v", err)
	}
	if err := wb.SetWorkspaceBlob(ctx, second); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased SetWorkspaceBlob: %v", err)
	}
}
