//go:build windows

package blackbox

import "testing"

// The real reader must report a folder's list as present, with entries. A first version read the "defaulted" return of
// SECURITY_DESCRIPTOR.DACL() as "present", so every real list looked absent and every proof refused (found on a PC, BL-140).
func TestDescribeFileDACLReadsARealList(t *testing.T) {
	present, aces, err := describeFileDACL(t.TempDir(), "a test folder")
	if err != nil {
		t.Fatalf("describeFileDACL: %v", err)
	}
	if !present {
		t.Fatal("a real folder's access list was reported as absent")
	}
	if len(aces) == 0 {
		t.Fatal("a real folder's access list came back with no entries")
	}
}

// The final-path step refuses what it cannot prove, before any file is read.
func TestResolveSettingsPathRefusals(t *testing.T) {
	for _, p := range []string{`\\server\share\blackbox.json`, `\\?\C:\x\blackbox.json`, `blackbox.json`, `relative\blackbox.json`} {
		if _, err := resolveSettingsPath(p); err == nil {
			t.Errorf("resolveSettingsPath(%q) = nil, want a refusal", p)
		}
	}
}
