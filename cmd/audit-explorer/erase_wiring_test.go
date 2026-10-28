package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/server"
)

// The Erased line is left only when the erase's own steps worked, and an erase with
// no other hook still leaves it.
func TestWithErasedLineLeavesALineOnlyAfterSuccess(t *testing.T) {
	id := casestore.CaseID("abcdefghijklmnopqrstuvwxyz")
	log := server.NewErasedFile(filepath.Join(t.TempDir(), "erased.json"))

	failing := withErasedLine(func(context.Context, casestore.CaseID) error { return errors.New("ledger busy") }, log)
	if err := failing(context.Background(), id); err == nil {
		t.Fatal("a failing hook must fail the erase")
	}
	if got := log.Lines(); len(got) != 0 {
		t.Fatalf("a line was left by a failed erase: %+v", got)
	}

	alone := withErasedLine(nil, log)
	if err := alone(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := log.Lines(); len(got) != 1 || got[0].ID != id {
		t.Fatalf("lines = %+v", got)
	}
}
