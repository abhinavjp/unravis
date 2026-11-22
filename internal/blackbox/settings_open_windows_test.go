//go:build windows

package blackbox

// BL-140 (b): the settings reader on real NTFS files with real access lists (the CI runner is an administrator). Fixtures that cannot be made
// are a t.Fatal, never a skip. The outer reader (OSEnv.ReadVerifiedSettings: path, drive and chain checks, then the handle proof) is tested
// with the inner one (readOpenedSettings: the handle proof alone), and every refusal is checked for its fixed word.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// lockedSettings writes data to a file in a fresh folder and locks it as the installer does (lockAdminOnlyFile).
func lockedSettings(t *testing.T, data []byte) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the reader wants the file's final path, with no 8.3 names
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "blackbox.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lockAdminOnlyFile(p); err != nil {
		t.Fatalf("lockAdminOnlyFile: %v", err)
	}
	return p
}

// mustRefuse checks a refusal: no bytes, an error that carries the fixed word, and a text that is not the word of another cause.
func mustRefuse(t *testing.T, got []byte, err error, why string, words ...string) {
	t.Helper()
	if err == nil || got != nil {
		t.Fatalf("%s: read %q, err %v; want a refusal and no bytes", why, got, err)
	}
	w := RefusalWord(err)
	for _, ok := range words {
		if w == ok {
			return
		}
	}
	t.Fatalf("%s: refused with word %q (%v), want one of %v", why, w, err, words)
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s (a junction needs no privilege, so a refusal here is a fact to fail on)", link, target, err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

// lockedWith writes a file in a fresh long-path folder and gives it the owner and list sddl names (the real owner write, as the installer's).
func lockedWith(t *testing.T, data []byte, owner, sddl string) string {
	t.Helper()
	p := lockedSettings(t, data)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	o, err := windows.StringToSid(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerAndProtectedDACL(p, o, dacl); err != nil {
		t.Fatalf("setting owner %s: %v", owner, err)
	}
	return p
}

// The whole reader the service will call, on a file locked as the installer locks it: path checks, chain check, handle proof, bytes; and
// the same bytes parse into a config through the pure helper, with nothing logged.
func TestReadVerifiedSettingsReadsALockedFile(t *testing.T) {
	want := []byte(`{"name":"x"}`)
	p := lockedSettings(t, want)
	got, err := OSEnv{}.ReadVerifiedSettings(p)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, %v; want %q", got, err, want)
	}
	var words []string
	cfg, err := ParseVerifiedSettings(OSEnv{}.ReadVerifiedSettings, p, func(w string) { words = append(words, w) })
	if err != nil || cfg.Name != "x" || len(words) != 0 {
		t.Fatalf("parsed %+v, %v, logged %v", cfg, err, words)
	}
	// The inner reader gives the same bytes.
	if got, err := readOpenedSettings(p); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("inner read %q, %v", got, err)
	}
}

func TestReadVerifiedSettingsRefusesWhatThePathChecksRefuse(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	for _, c := range []struct{ name, path, word string }{
		{"a relative path", `blackbox.json`, WordPath},
		{"a UNC path", `\\server\share\blackbox.json`, WordPath},
		{"a device path", `\\?\` + p, WordPath},
		{"a missing file", filepath.Join(filepath.Dir(p), "none.json"), WordNotFound},
	} {
		got, err := OSEnv{}.ReadVerifiedSettings(c.path)
		mustRefuse(t, got, err, c.name, c.word)
	}
}

func TestReadVerifiedSettingsRefusesAJunctionLeaf(t *testing.T) {
	dir := filepath.Dir(lockedSettings(t, []byte(`{}`)))
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	junction(t, link, target)
	got, err := OSEnv{}.ReadVerifiedSettings(link)
	mustRefuse(t, got, err, "a junction named like the settings file", WordChain)
}

func TestReadOpenedSettingsRefusesAReparseLeaf(t *testing.T) {
	dir := filepath.Dir(lockedSettings(t, []byte(`{}`)))
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	junction(t, link, target)
	// The inner reader opens the link itself (OPEN_REPARSE_POINT), so the handle rule is what refuses it.
	got, err := readOpenedSettings(link)
	mustRefuse(t, got, err, "a junction at the handle", WordFolder, WordLink)
}

func TestReadOpenedSettingsRefusesANonAdminOwner(t *testing.T) {
	// The list is administrators-only; only the owner is wrong (Users). An owner holds WRITE_DAC whatever the list says.
	p := lockedWith(t, []byte(`{}`), "S-1-5-32-545", "D:PAI(A;;FA;;;SY)(A;;FA;;;BA)")
	got, err := OSEnv{}.ReadVerifiedSettings(p)
	mustRefuse(t, got, err, "a file owned by Users", WordOwner)
}

func TestReadOpenedSettingsRefusesAFileOverTheLimit(t *testing.T) {
	p := lockedSettings(t, bytes.Repeat([]byte("a"), maxConfigBytes+1))
	got, err := readOpenedSettings(p)
	mustRefuse(t, got, err, "a file over the limit", WordTooLarge)
	// Exactly the limit is read whole (it is not valid JSON, but that is the parser's refusal, not this reader's).
	p2 := lockedSettings(t, bytes.Repeat([]byte("a"), maxConfigBytes))
	if got, err := readOpenedSettings(p2); err != nil || len(got) != maxConfigBytes {
		t.Fatalf("a file of exactly the limit: %d bytes, %v", len(got), err)
	}
}

func TestReadOpenedSettingsRefusesAPathThroughAJunction(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	via := filepath.Join(t.TempDir(), "via")
	junction(t, via, filepath.Dir(p))
	got, err := readOpenedSettings(filepath.Join(via, filepath.Base(p)))
	mustRefuse(t, got, err, "a folder above is a junction", WordMoved)
}

func TestReadOpenedSettingsMissingFileIsRefused(t *testing.T) {
	dir := filepath.Dir(lockedSettings(t, []byte(`{}`)))
	got, err := readOpenedSettings(filepath.Join(dir, "none.json"))
	mustRefuse(t, got, err, "a missing file", WordNotFound)
}

// Another program holds the file without sharing it. The open is retried a few times, the same way each time, and then refused with its word.
func TestReadOpenedSettingsRetriesASharingViolation(t *testing.T) {
	want := []byte(`{"name":"later"}`)
	p := lockedSettings(t, want)
	hold := func() windows.Handle {
		name, _ := windows.UTF16PtrFromString(p)
		h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	oldSleep := settingsSleep
	t.Cleanup(func() { settingsSleep = oldSleep })

	// Released at the first pause: the read then succeeds.
	h := hold()
	pauses := 0
	settingsSleep = func(time.Duration) {
		pauses++
		if h != 0 {
			windows.CloseHandle(h)
			h = 0
		}
	}
	got, err := readOpenedSettings(p)
	if err != nil || !bytes.Equal(got, want) || pauses != 1 {
		t.Fatalf("read %q, %v after %d pause(s); want the bytes after exactly one", got, err, pauses)
	}

	// Never released: three pauses, then a refusal with its word, and no bytes.
	h = hold()
	t.Cleanup(func() {
		if h != 0 {
			windows.CloseHandle(h)
		}
	})
	pauses = 0
	settingsSleep = func(time.Duration) { pauses++ }
	got, err = readOpenedSettings(p)
	mustRefuse(t, got, err, "a file that is never released", WordSharing)
	if pauses != settingsOpenRetries {
		t.Fatalf("%d pauses, want %d", pauses, settingsOpenRetries)
	}
}

// The cause is only a word: whatever the file or the path holds never reaches the log.
func TestRefusalWordsDoNotCarryThePath(t *testing.T) {
	p := lockedWith(t, []byte(`{}`), "S-1-5-32-545", "D:PAI(A;;FA;;;SY)(A;;FA;;;BA)")
	var words []string
	_, err := ParseVerifiedSettings(OSEnv{}.ReadVerifiedSettings, p, func(w string) { words = append(words, w) })
	if !errors.Is(err, ErrSettingsNotProven) || len(words) != 1 || words[0] != WordOwner {
		t.Fatalf("%v, logged %v", err, words)
	}
	if strings.Contains(err.Error(), filepath.Base(p)) || strings.ContainsAny(words[0], `\/:`) {
		t.Fatalf("the refusal or its word leaks the path: %q %q", err.Error(), words[0])
	}
}

func TestReadOpenedSettingsRefusesAHardLink(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	if err := os.Link(p, p+".2"); err != nil {
		t.Fatal(err)
	}
	got, err := readOpenedSettings(p)
	if err == nil || got != nil {
		t.Fatalf("a file with two names was read: %q, %v", got, err)
	}
}

func TestReadOpenedSettingsRefusesAUsersWritableList(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	sd, err := windows.SecurityDescriptorFromString("D:PAI(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerAndProtectedDACL(p, owner, dacl); err != nil {
		t.Fatal(err)
	}
	if got, err := readOpenedSettings(p); err == nil || got != nil {
		t.Fatalf("a list that lets Users change the file was accepted: %q, %v", got, err)
	}
}

func TestReadOpenedSettingsHoldsTheFileWhileReading(t *testing.T) {
	want := []byte(`{"name":"proven"}`)
	p := lockedSettings(t, want)
	ran := false
	afterSettingsOpen = func() {
		ran = true
		if err := os.Rename(p, p+".moved"); err == nil {
			t.Error("the file was renamed away between the proof and the read")
		}
		if err := os.Remove(p); err == nil {
			t.Error("the file was deleted between the proof and the read")
		}
		if f, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
			f.Close()
			t.Error("the file was opened for writing between the proof and the read")
		}
	}
	t.Cleanup(func() { afterSettingsOpen = nil })
	got, err := readOpenedSettings(p)
	if !ran {
		t.Fatal("the hook did not run")
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, %v; want %q", got, err, want)
	}
}
