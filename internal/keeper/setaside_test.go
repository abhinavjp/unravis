package keeper

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/history"
)

// RV-DAT-1 (W-13): the keeper's saved MongoDB file can be set aside through mongo.setaside, and the two refusals reach the caller as the
// history errors (a healthy file, and a fourth copy).
func TestMongoSetAsideThroughTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	path := filepath.Join(t.TempDir(), "connections.dat")
	svc, err := history.New(path, xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	h.backend.Mongo = svc
	m := NewMongoStore(h.client)

	if _, err := m.SetAside(); !errors.Is(err, history.ErrNotDamaged) {
		t.Fatalf("a missing file: %v, want ErrNotDamaged", err)
	}
	if _, err := m.Remember("Audit prod", mongoLink, "events", false); err != nil {
		t.Fatal(err)
	}
	healthy, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAside(); !errors.Is(err, history.ErrNotDamaged) {
		t.Fatalf("a healthy file: %v, want ErrNotDamaged", err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, healthy) {
		t.Fatalf("a refused set-aside changed the healthy file: %v", err)
	}

	damaged := []byte("this is not a connections file")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.List(); !errors.Is(err, history.ErrCorrupt) {
		t.Fatalf("a damaged file listed as %v, want ErrCorrupt", err)
	}
	name, err := m.SetAside()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(filepath.Dir(path), name)); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the kept copy %q differs from the damaged file: %v", name, err)
	}
	if items, err := m.List(); err != nil || len(items) != 0 {
		t.Fatalf("after set-aside: %v %v, want an empty list", items, err)
	}

	// Two more copies fill the three, and a fourth is refused with the history error and nothing removed.
	for i := 0; i < history.MaxSetAside-1; i++ {
		if err := os.WriteFile(path, []byte("damaged again"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetAside(); err != nil {
			t.Fatalf("set-aside %d: %v", i+2, err)
		}
	}
	if err := os.WriteFile(path, []byte("the fourth"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAside(); !errors.Is(err, history.ErrSetAsideFull) {
		t.Fatalf("a fourth set-aside: %v, want ErrSetAsideFull", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "the fourth" {
		t.Fatalf("the fourth damaged file was moved: %v", err)
	}
}

// The shared list names the keeper's own folder (so the page can say where the kept file is) and says it is shared.
func TestMongoStoreNamesTheKeeperFolder(t *testing.T) {
	h := newHarness(t, nil)
	if dir, shared := NewMongoStore(h.client).WithFolder(`C:\ProgramData\Unravis\keeper`).Folder(); dir != `C:\ProgramData\Unravis\keeper` || !shared {
		t.Fatalf("Folder = %q %v", dir, shared)
	}
}
