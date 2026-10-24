//go:build windows

package sqlite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

func junction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) }) // the link only, never the target
}

func TestNewRefusesJunctionRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "cases")
	junction(t, link, target)
	_, err := New(Options{Root: link, Protector: storetest.RandomProtector(), MinFreeBytes: -1})
	if !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("New with a junction root = %v, want ErrReparsePoint", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("something was written through the junction: %d entries", len(entries))
	}
}

func TestJunctionCaseFolderIsNeverFollowed(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	target := filepath.Join(filepath.Dir(root), "precious")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = casestore.CaseID("aaaaaaaaaaaaaaaaaaaaaaaaaa")
	junction(t, filepath.Join(root, string(id)), target)

	if _, err := s.Open(ctx, id); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Errorf("Open through a junction = %v, want ErrReparsePoint", err)
	}
	if err := s.Erase(ctx, id); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Errorf("Erase through a junction = %v, want ErrReparsePoint", err)
	}
	if err := s.CloseCase(ctx, id); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Errorf("CloseCase through a junction = %v, want ErrReparsePoint", err)
	}
	if infos, err := s.List(ctx); err != nil || len(infos) != 0 {
		t.Errorf("List listed a junction: %+v, %v", infos, err)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "x" {
		t.Fatalf("the junction target was touched: %v", err)
	}
}
