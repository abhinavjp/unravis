//go:build windows

package blackbox

// SEC-2b: the directory swap the SQL data folder is handed through.
//
// secureSQLDir borrows SeRestorePrivilege/SeTakeOwnershipPrivilege and writes a
// protected DACL into the path it is given. If any component of that path is a
// junction, the write lands on the folder the junction points at, not the one the
// operator named, so an attacker who can plant a link under a folder the install
// trusts gets any folder on the machine re-secured to the SQL SID. The ancestor
// walk is the only thing standing between the two, and until this file nothing
// proved it: the SQL-chain refusal at sqlservice_windows.go ("refusing a reparse
// point in the SQL data folder chain") had no test naming it anywhere in the
// repository, and the reparse test that does exist (internal/casestore) covers a
// different function.
//
// Deliberately unmocked. fileAttributes is the real GetFileAttributesW, the
// junction is real, and the owner read is the real one, so this exercises the
// guard rather than the shape of a fake. The helper does not skip when a junction
// cannot be made: mklink /J needs no privilege, and a silently-skipped assertion
// would leave this file reporting a pass it never ran.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sqlDataChainSwapSentinel is the sentence the guard writes. It is repeated here
// on purpose: a test that only checks "some error" passes when the refusal comes
// from an unrelated rule, which is how this guard could go missing unnoticed.
const sqlDataChainSwapSentinel = "refusing a reparse point in the SQL data folder chain"

func makeJunctionForSwapTest(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s - a junction needs no privilege, so this box refusing to make one is a fact worth failing on rather than skipping past",
			link, target, err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { _ = os.Remove(link) }) // removes the junction, never the target
}

func TestSecureSQLDirRefusesASwappedFolderBeforeWritingIntoIt(t *testing.T) {
	base := t.TempDir()
	// The folder the install believes it is securing, and the folder the junction
	// really points at.
	target := filepath.Join(base, "target")
	sql := filepath.Join(target, "sql")
	if err := os.MkdirAll(sql, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	makeJunctionForSwapTest(t, link, target)
	// ownerSID and the DACL both read through the junction, so one pair of reads
	// on the real path is enough to see whether anything was written.
	ownerBefore, err := ownerSID(sql)
	if err != nil {
		t.Fatal(err)
	}

	err = secureSQLDir(filepath.Join(link, "sql"), "S-1-5-80-0000000000-0000000000-0000000000-0000000010")
	if err == nil {
		t.Fatalf("secureSQLDir accepted %s: the folder was reached through a junction, so the DACL it wrote landed on %s, which nobody asked it to secure", filepath.Join(link, "sql"), sql)
	}
	if !strings.Contains(err.Error(), sqlDataChainSwapSentinel) {
		t.Fatalf("err = %v, want the swap refusal %q: any other sentence means the install was stopped by a rule that has nothing to do with the junction, and the junction guard itself is still untested", err, sqlDataChainSwapSentinel)
	}
	ownerAfter, err := ownerSID(sql)
	if err != nil {
		t.Fatal(err)
	}
	if ownerAfter != ownerBefore {
		t.Errorf("the swapped folder's owner went from %q to %q even though the install reported a refusal: the write happened and the error came after it", ownerBefore, ownerAfter)
	}

	// The control that makes the sentence above mean something: the same folder
	// reached without a junction in its chain must not be refused as a swap. What
	// stops it here is the ancestor-owner rule, because this host's drive root is
	// owned by NT SERVICE\\TrustedInstaller - which is exactly why the refusal has
	// to name the junction and not just any failure.
	if err := secureSQLDir(sql, "S-1-5-80-0000000000-0000000000-0000000000-0000000010"); err != nil && strings.Contains(err.Error(), sqlDataChainSwapSentinel) {
		t.Errorf("a plain folder with no reparse point in its chain was refused as a swap: %v", err)
	}
}
