package masterkey

// TSK-21 Go fixes (security L2): readDoc opens the file once, refuses a link or
// reparse point, and reads through a hard cap.

import (
	"os"
	"path/filepath"
	"testing"
)

// A real file followed by padding that pushes it one byte past the cap is
// damaged, though the JSON at its start is perfect: the cap decides, not the
// content. Padding that stays inside the cap does not hurt.
func TestP06_ReadDocCapsTheFileAtFileMax(t *testing.T) {
	dir := setup(t, goodPass)
	good, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	pad := func(n int) []byte {
		b := append([]byte{}, good...)
		for len(b) < n {
			b = append(b, ' ')
		}
		return b
	}
	if err := os.WriteFile(Path(dir), pad(fileMax), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(dir); got != FileUsable {
		t.Fatalf("file of exactly fileMax bytes = %v, want FileUsable", got)
	}
	if err := os.WriteFile(Path(dir), pad(fileMax+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(dir); got != FileDamaged {
		t.Fatalf("file of fileMax+1 bytes = %v, want FileDamaged", got)
	}
	if _, err := Unlock(dir, []byte(goodPass)); err != ErrDamaged {
		t.Fatalf("Unlock = %v, want ErrDamaged", err)
	}
	// A far larger file is refused the same way (and never read whole).
	if err := os.WriteFile(Path(dir), pad(1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(dir); got != FileDamaged {
		t.Fatalf("1 MiB file = %v, want FileDamaged", got)
	}
}

// A link standing in for masterkey.dat is not followed, even to a perfect file.
func TestP06_ReadDocRefusesALink(t *testing.T) {
	dir := setup(t, goodPass)
	real := filepath.Join(t.TempDir(), "elsewhere.dat")
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(Path(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, Path(dir)); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if got := Inspect(dir); got != FileDamaged {
		t.Fatalf("Inspect through a link = %v, want FileDamaged", got)
	}
	if _, err := Unlock(dir, []byte(goodPass)); err != ErrDamaged {
		t.Fatalf("Unlock through a link = %v, want ErrDamaged", err)
	}
}
