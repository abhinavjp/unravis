//go:build windows

package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The folder an elevated answer is written to is private (F4): protected from
// inherited entries, and no entry for ordinary users or Everyone.
func TestAnswerSlotFolderIsProtectedAndOurs(t *testing.T) {
	dir, err := os.MkdirTemp("", "unravis-bb-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	slot, err := openAnswerSlot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer slot.Close()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.Contains(s, "D:P") {
		t.Errorf("the entries are not protected from inheritance: %s", s)
	}
	for _, bad := range []string{";;;BU)", ";;;WD)", ";;;AU)"} {
		if strings.Contains(s, bad) {
			t.Errorf("the folder is open to a wider group (%s): %s", bad, s)
		}
	}
}

// SEC-FIX-04: the answer file exists before the helper runs, cannot be renamed,
// deleted or swapped while the app holds it, and the helper can still fill it.
func TestAnswerSlotIsHeldAndTheHelperCanFillIt(t *testing.T) {
	dir, err := os.MkdirTemp("", "unravis-bb-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	slot, err := openAnswerSlot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slot.Read(); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("an empty answer file: %v", err)
	}
	if err := os.Remove(slot.Path); err == nil {
		t.Fatal("the answer file could be deleted while held")
	}
	if err := os.Rename(slot.Path, slot.Path+".x"); err == nil {
		t.Fatal("the answer file could be renamed while held")
	}
	if err := os.Rename(dir, dir+"-moved"); err == nil {
		os.Rename(dir+"-moved", dir)
		t.Fatal("the answer folder could be renamed while held")
	}
	// The helper side: open the existing file only.
	f, err := OpenAnswerFile(slot.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ok":true}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := slot.Read()
	if err != nil || !strings.Contains(string(got), `"ok":true`) {
		t.Fatalf("%q %v", got, err)
	}
	slot.Close()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("the folder could not be removed after the handles closed: %v", err)
	}
}

// SEC-FIX-04: a folder swapped for a junction is refused, not followed.
func TestAnswerSlotRefusesAJunction(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := makeJunction(link, target); err != nil {
		t.Skipf("cannot make a junction here: %v", err)
	}
	if slot, err := openAnswerSlot(link); err == nil {
		slot.Close()
		t.Fatal("a junction was accepted as the answer folder")
	}
	if _, err := os.Stat(filepath.Join(target, "answer.json")); err == nil {
		t.Fatal("a file was created through the junction")
	}
}

// The elevated side never creates the file: a missing one is an error.
func TestOpenAnswerFileNeedsAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "answer.json")
	if f, err := OpenAnswerFile(p); err == nil {
		f.Close()
		t.Fatal("a missing answer file was created")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("the file exists now")
	}
}

// ... and a link in its place is refused.
func TestOpenAnswerFileRefusesALink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real.txt")
	if err := os.WriteFile(real, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "answer.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	if f, err := OpenAnswerFile(link); err == nil {
		f.Close()
		t.Fatal("a symbolic link was opened as the answer file")
	}
	if b, _ := os.ReadFile(real); string(b) != "keep" {
		t.Fatalf("the link target was changed: %q", b)
	}
}
