package blackbox

import (
	"bytes"
	"strings"
	"testing"
)

// Scenario 1 defect tests for lane L8 (Blackbox recorder). Helpers (packCfg, unzipAll, memProgram, newSrvRig, newRig) are in pack_test.go, server_test.go and recorder_test.go.

// D-B-43: the README step is the same command the page shows: install, with no --name (the name is in blackbox.json).
func TestScn1L8PackReadmeInstallCommandHasNoName(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, packCfg(), memProgram{[]byte("MZ-fake-program")}); err != nil {
		t.Fatal(err)
	}
	readme := string(unzipAll(t, buf.Bytes())[PackReadmeName])
	if readme == "" {
		t.Fatal("no README.txt in the pack")
	}
	if !strings.Contains(readme, "3. Run:   .\\unravis-blackbox.exe install\r\n") {
		t.Fatalf("step 3 is not the plain install command:\n%s", readme)
	}
	if strings.Contains(readme, "--name") {
		t.Fatalf("the README still tells the user to pass --name:\n%s", readme)
	}
	// Step 1 still names the server, so the person knows which server the pack is for.
	if !strings.Contains(readme, "1. Copy this folder to the server "+packCfg().Name+" and unzip it there.") {
		t.Fatalf("step 1 changed:\n%s", readme)
	}
}
