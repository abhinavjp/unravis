//go:build windows

package setup

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These tests run the real reader on real NTFS files with real access lists (the CI runner is an administrator). A fixture that cannot
// be made (a junction, a hard link, an owner) is a t.Fatal, never a skip: a skipped proof would pass for a reader that proves nothing.

const adminOnlyFileSDDL = "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)"

// longTempDir is t.TempDir() in its long form: the reader compares the path with the real path Windows reports, which has no 8.3
// names, and the runner's temp folder is spelled RUNNER~1.
func longTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Fatalf("GetLongPathName(%s): %d, %v", dir, n, err)
	}
	return windows.UTF16ToString(buf[:n])
}

// enableRestorePrivilege lets this process name any account as a file's owner for the rest of the test run.
func enableRestorePrivilege(t *testing.T) {
	t.Helper()
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		t.Fatal(err)
	}
	defer tok.Close()
	name, _ := windows.UTF16PtrFromString("SeRestorePrivilege")
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		t.Fatal(err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	if err := windows.AdjustTokenPrivileges(tok, false, &tp, uint32(unsafe.Sizeof(tp)), nil, nil); err != nil {
		t.Fatal(err)
	}
}

// proofFile writes data to dir\name and gives it the owner and list sddl names.
func proofFile(t *testing.T, dir, name string, data []byte, sddl string) string {
	t.Helper()
	enableRestorePrivilege(t)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (osSecurity{}).SetSDDL(p, sddl); err != nil {
		t.Fatalf("SetSDDL(%s, %s): %v", p, sddl, err)
	}
	return p
}

func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s (a junction needs no privilege, so a refusal here is a fact to fail on)", link, target, err, out)
	}
}

func mustRefuse(t *testing.T, got []byte, err error, why string) {
	t.Helper()
	if err == nil || got != nil {
		t.Fatalf("%s: read %q, err %v; want a refusal and no bytes", why, got, err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s: refused as \"not there\": %v", why, err)
	}
}

func TestReadFileProvenReadsAnAdminOnlyFile(t *testing.T) {
	dir := longTempDir(t)
	want := []byte(`{"op":"upgrade"}`)
	p := proofFile(t, dir, "journal.json", want, adminOnlyFileSDDL)
	got, err := (osSecurity{}).ReadFileProven(p, 1024)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, err %v, want %q", got, err, want)
	}
	// Exactly max bytes is fine; one byte less is a refusal.
	if got, err := (osSecurity{}).ReadFileProven(p, int64(len(want))); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("a file of exactly max bytes: %q, %v", got, err)
	}
}

func TestReadFileProvenMissingFileIsNotThere(t *testing.T) {
	dir := longTempDir(t)
	if _, err := (osSecurity{}).ReadFileProven(filepath.Join(dir, "none.json"), 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file gave %v, want ErrNotExist", err)
	}
	// A missing folder above the file is not "no journal": nobody proved that path.
	if _, err := (osSecurity{}).ReadFileProven(filepath.Join(dir, "gone", "journal.json"), 1024); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a missing folder gave %v, want ErrNotProven", err)
	}
}

func TestReadFileProvenRefusesAHardLink(t *testing.T) {
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", []byte("x"), adminOnlyFileSDDL)
	other := filepath.Join(dir, "second-name.json")
	if err := os.Link(p, other); err != nil {
		t.Fatalf("os.Link: %v", err)
	}
	for _, name := range []string{p, other} {
		got, err := (osSecurity{}).ReadFileProven(name, 1024)
		mustRefuse(t, got, err, "a hard link ("+filepath.Base(name)+")")
		if !errors.Is(err, ErrNotProven) {
			t.Fatalf("a hard link gave %v, want ErrNotProven", err)
		}
	}
}

func TestReadFileProvenRefusesAJunctionLeaf(t *testing.T) {
	dir := longTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	proofFile(t, target, "journal.json", []byte("x"), adminOnlyFileSDDL)
	link := filepath.Join(dir, "journal.json")
	mklinkJunction(t, link, target)
	got, err := (osSecurity{}).ReadFileProven(link, 1024)
	mustRefuse(t, got, err, "a junction named journal.json")
}

func TestReadFileProvenRefusesAFileSymlink(t *testing.T) {
	dir := longTempDir(t)
	real := proofFile(t, dir, "real.json", []byte("x"), adminOnlyFileSDDL)
	link := filepath.Join(dir, "journal.json")
	if err := os.Symlink(real, link); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("os.Symlink: %v (the runner must be able to make one)", err)
		}
		t.Skipf("this box cannot make a symlink: %v", err)
	}
	got, err := (osSecurity{}).ReadFileProven(link, 1024)
	mustRefuse(t, got, err, "a file symlink")
	if !errors.Is(err, ErrNotProven) {
		t.Fatalf("a file symlink gave %v, want ErrNotProven", err)
	}
}

func TestReadFileProvenRefusesAFileThroughAJunctionFolder(t *testing.T) {
	dir := longTempDir(t)
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	proofFile(t, real, "journal.json", []byte("x"), adminOnlyFileSDDL)
	jn := filepath.Join(dir, "jn")
	mklinkJunction(t, jn, real)
	got, err := (osSecurity{}).ReadFileProven(filepath.Join(jn, "journal.json"), 1024)
	mustRefuse(t, got, err, "a file reached through a junction folder")
	if !errors.Is(err, ErrNotProven) {
		t.Fatalf("a junction folder above gave %v, want ErrNotProven", err)
	}
}

func TestReadFileProvenRefusesAUsersWritableFile(t *testing.T) {
	for name, sddl := range map[string]string{
		"full control":    "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;BU)",
		"write data":      "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;0x2;;;BU)",
		"change the list": "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;WD;;;BU)",
		"take ownership":  "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;WO;;;BU)",
		"authenticated":   "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;GW;;;AU)",
	} {
		t.Run(name, func(t *testing.T) {
			dir := longTempDir(t)
			p := proofFile(t, dir, "journal.json", []byte("x"), sddl)
			got, err := (osSecurity{}).ReadFileProven(p, 1024)
			mustRefuse(t, got, err, name)
			if !errors.Is(err, ErrNotProven) {
				t.Fatalf("%s gave %v, want ErrNotProven", name, err)
			}
		})
	}
	// A read-only entry for Users is fine: it is the shape S2c would give the folder.
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", []byte("x"), "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)")
	if got, err := (osSecurity{}).ReadFileProven(p, 1024); err != nil || string(got) != "x" {
		t.Fatalf("a read-only entry for Users was refused: %q, %v", got, err)
	}
}

func TestReadFileProvenRefusesAUserOwnedFile(t *testing.T) {
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", []byte("x"), "O:BUD:P(A;;FA;;;SY)(A;;FA;;;BA)")
	got, err := (osSecurity{}).ReadFileProven(p, 1024)
	mustRefuse(t, got, err, "owner Users")
	if !errors.Is(err, ErrNotProven) {
		t.Fatalf("owner Users gave %v, want ErrNotProven", err)
	}
}

// The account the elevated copy runs as is not trusted as an owner: an owner can rewrite the access list without any entry saying so.
func TestReadFileProvenRefusesAFileOwnedByTheRunningAccount(t *testing.T) {
	self, err := CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if self == "S-1-5-18" || self == "S-1-5-32-544" {
		t.Skipf("this process runs as %s, which is a trusted owner anyway", self)
	}
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", []byte("x"), "O:"+self+"D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	got, err := (osSecurity{}).ReadFileProven(p, 1024)
	mustRefuse(t, got, err, "owner is the running account")
	if !errors.Is(err, ErrNotProven) {
		t.Fatalf("owner = running account gave %v, want ErrNotProven", err)
	}
}

func TestReadFileProvenRefusesTooLarge(t *testing.T) {
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", bytes.Repeat([]byte("a"), 17), adminOnlyFileSDDL)
	got, err := (osSecurity{}).ReadFileProven(p, 16)
	mustRefuse(t, got, err, "max+1 bytes")
	if !errors.Is(err, ErrNotProven) {
		t.Fatalf("a file over max gave %v, want ErrNotProven", err)
	}
	for _, bad := range []int64{0, -1} {
		if _, err := (osSecurity{}).ReadFileProven(p, bad); !errors.Is(err, ErrNotProven) {
			t.Fatalf("max %d gave %v, want ErrNotProven", bad, err)
		}
	}
}

func TestReadFileProvenRefusesAFolder(t *testing.T) {
	dir := longTempDir(t)
	got, err := (osSecurity{}).ReadFileProven(dir, 1024)
	mustRefuse(t, got, err, "a folder")
}

func TestReadFileProvenRefusesAFileAnotherProgramHoldsOpenForWriting(t *testing.T) {
	dir := longTempDir(t)
	p := proofFile(t, dir, "journal.json", []byte("x"), adminOnlyFileSDDL)
	w, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	got, err := (osSecurity{}).ReadFileProven(p, 1024)
	mustRefuse(t, got, err, "a file held open for writing (share-read only must not open past a writer)")
}

// Between the proof and the read nobody can change, rename, delete or swap the file: the handle is share-read only, and the bytes come
// from that handle, so a swap that did happen would not reach the caller.
func TestReadFileProvenHoldsTheFileWhileReading(t *testing.T) {
	dir := longTempDir(t)
	want := []byte("the proven bytes")
	p := proofFile(t, dir, "journal.json", want, adminOnlyFileSDDL)
	evil := proofFile(t, dir, "evil.json", []byte("evil bytes of a person"), adminOnlyFileSDDL)

	var hookRan bool
	var attempts []string
	try := func(what string, err error) {
		if err == nil {
			attempts = append(attempts, what+" SUCCEEDED")
		}
	}
	afterProofHook = func() {
		hookRan = true
		try("rename the file away", os.Rename(p, p+".moved"))
		try("rename another file over it", os.Rename(evil, p))
		try("delete it", os.Remove(p))
		w, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
		if err == nil {
			w.Close()
		}
		try("open it for writing", err)
	}
	t.Cleanup(func() { afterProofHook = nil })

	got, err := (osSecurity{}).ReadFileProven(p, 1024)
	if !hookRan {
		t.Fatal("the hook never ran: the reader no longer calls afterProofHook between the proof and the read")
	}
	if len(attempts) != 0 {
		t.Fatalf("the file was not held while it was read: %s", strings.Join(attempts, "; "))
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, err %v, want %q", got, err, want)
	}
	if b, err := os.ReadFile(p); err != nil || !bytes.Equal(b, want) {
		t.Fatalf("the file changed during the read: %q, %v", b, err)
	}
}

// BL-183 (a) review SHOULD: the machine journal the real writer makes must be one the real reader accepts. Nothing here forces an
// owner: the file gets what Journal.save gives it on this runner (an elevated administrator).

func realWriter() *journalWriter {
	return &journalWriter{sec: osSecurity{}, sddl: MachineJournalFileSDDL}
}

// machineRig is a ProgramData-like folder with the real journal folder inside it, made as setup makes it.
func machineRig(t *testing.T) (r *Runner, journalPath string) {
	t.Helper()
	pd := longTempDir(t)
	r = &Runner{Sys: &System{Sec: osSecurity{}}, Dirs: Dirs{ProgramData: pd}}
	dir := MachineJournalDir(r.Dirs)
	if err := (osSecurity{}).MkdirSDDL(dir, AdminOnlySDDL); err != nil {
		t.Fatal(err)
	}
	return r, filepath.Join(dir, JournalName)
}

func TestTheRealJournalWriterMakesAFileTheRealReaderAccepts(t *testing.T) {
	_, p := machineRig(t)
	j, err := NewJournalWith(p, OpUpgrade, "1.2.0", time.Now(), LoadJournal, realWriter())
	if err != nil {
		t.Fatal(err)
	}
	// A second and a third write replace the file by a rename: the owner must survive that.
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := j.Record(Undo{Step: "s", Kind: UndoRemoveFile, Path: filepath.Join(filepath.Dir(p), name)}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := (osSecurity{}).ReadFileProven(p, maxJournalBytes)
	if err != nil {
		t.Fatalf("the reader refused the file the writer made: %v", err)
	}
	back, err := ParseJournal(data, p)
	if err != nil || len(back.f.Undos) != 2 {
		t.Fatalf("read back %v, %v", back, err)
	}
	sddl, err := (osSecurity{}).SDDL(p)
	if err != nil || !strings.HasPrefix(sddl, "O:BA") {
		t.Fatalf("owner of the real journal: %q, %v", sddl, err)
	}
	// Marking it finished writes it again; still readable.
	if _, err := j.Commit(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverJournalOnRealNTFS(t *testing.T) {
	r, p := machineRig(t)
	victim := filepath.Join(longTempDir(t), "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := NewJournalWith(p, OpUpgrade, "1.2.0", time.Now(), LoadJournal, realWriter())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(Undo{Step: "s", Kind: UndoRemoveFile, Path: victim, Note: "test file"}); err != nil {
		t.Fatal(err)
	}
	if !r.JournalPending(true) {
		t.Fatal("a proven pending journal is not pending")
	}
	found, left, err := r.RecoverJournal(true)
	if !found || err != nil || len(left) != 0 {
		t.Fatalf("recover: %v %v %v", found, left, err)
	}
	if _, err := os.Stat(victim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the listed file was not removed: %v", err)
	}
	if r.JournalPending(true) {
		t.Fatal("the replayed journal is still pending")
	}
}

func TestRecoverJournalOnRealNTFSRefusesWhatIsNotProven(t *testing.T) {
	// Users can write the journal folder: nothing is read.
	r, p := machineRig(t)
	victim := filepath.Join(longTempDir(t), "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := NewJournalWith(p, OpUpgrade, "1.2.0", time.Now(), LoadJournal, realWriter())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(Undo{Step: "s", Kind: UndoRemoveFile, Path: victim}); err != nil {
		t.Fatal(err)
	}
	if err := (osSecurity{}).SetSDDL(filepath.Dir(p), AdminOnlySDDL+"(A;OICI;FA;;;BU)"); err != nil {
		t.Fatal(err)
	}
	if found, _, err := r.RecoverJournal(true); !found || err == nil {
		t.Fatalf("a folder Users can write was used: %v %v", found, err)
	}
	if !r.JournalPending(true) {
		t.Fatal("a refused journal does not count as pending")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the refused journal was replayed: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the refused journal was deleted: %v", err)
	}

	// A journal written the old way (os.WriteFile as the token's default owner, with a Users-writable list) is refused too, and kept.
	r2, p2 := machineRig(t)
	if err := os.WriteFile(p2, []byte(`{"op":"upgrade"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (osSecurity{}).SetSDDL(p2, "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;BU)"); err != nil {
		t.Fatal(err)
	}
	if found, _, err := r2.RecoverJournal(true); !found || err == nil || !strings.Contains(err.Error(), "only administrators made") {
		t.Fatalf("a Users-writable journal was used: %v %v", found, err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatalf("the refused journal was deleted: %v", err)
	}

	// A hard link to the journal is refused (two names).
	r3, p3 := machineRig(t)
	if _, err := NewJournalWith(p3, OpUpgrade, "1.2.0", time.Now(), LoadJournal, realWriter()); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(p3, p3+".2"); err != nil {
		t.Fatal(err)
	}
	if found, _, err := r3.RecoverJournal(true); !found || err == nil {
		t.Fatalf("a hard-linked journal was used: %v %v", found, err)
	}
}

// BL-183 review of #105, SHOULD 3: what owner does a journal get when an older build (or anything) writes it with a plain os.WriteFile in the
// administrators-only folder, with nothing forced? The CI runner answers here for its own configuration (an elevated administrator). The
// test logs the owner, then checks the reader agrees with the owner rule, and that a refusal names the file and a way out.
func TestAPlainWriteInTheAdminOnlyFolderIsJudgedByItsRealOwner(t *testing.T) {
	r, p := machineRig(t)
	if err := os.WriteFile(p, []byte(`{"op":"upgrade","version":"1.2.0","started":"2026-11-22T10:00:00Z","undos":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sddl, err := (osSecurity{}).SDDL(p)
	if err != nil {
		t.Fatal(err)
	}
	// The SDDL also names the group (G:), so it is parsed, not cut at "D:". CI showed O:BA with a domain-users group for an elevated runner.
	parsed, err := ParseSDDL(sddl)
	if err != nil {
		t.Fatalf("cannot read %q: %v", sddl, err)
	}
	owner := parsed.Owner
	t.Logf("a journal written by a plain os.WriteFile in the administrators-only folder gets owner %q (SDDL %s)", owner, sddl)
	_, found, err := r.readJournal(true)
	if !found {
		t.Fatalf("the file is there but readJournal says it is not: %v", err)
	}
	ownerOK := journalFileOwnerOK(owner)
	switch {
	case ownerOK && err != nil:
		t.Fatalf("owner %q is accepted by the rule but the journal was refused: %v", owner, err)
	case !ownerOK && err == nil:
		t.Fatalf("owner %q is not accepted by the rule but the journal was read", owner)
	case !ownerOK:
		if !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "do not delete it") {
			t.Fatalf("the refusal does not name the file and a way out: %v", err)
		}
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("a refused journal was deleted: %v", serr)
		}
	}
}
