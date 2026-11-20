package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/update"
)

// upgradeTo makes the next upgrade install version ver, with setup program content "setup program <ver>".
func (e *testEnv) upgradeTo(ver string) {
	e.t.Helper()
	e.r.Payload = testPayload(e.t, ver, true)
	writeFile(e.t, e.r.SetupExe, "setup program "+ver)
	e.f.Versions[strings.ToLower(filepath.Join(e.userOpts().Location, AppExeName))] = "unravis " + ver
}

func TestUpgradeKeepsTheOldSetupAndAMarker(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	prev := filepath.Join(NewLayout(o, e.dirs).InstallDir, update.PreviousDir)
	if exists(prev) {
		t.Fatal("a first install keeps nothing")
	}
	e.upgradeTo("1.3.0")
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	m, err := update.ReadMarker(prev)
	if err != nil || m.From != "1.2.0" || m.To != "1.3.0" {
		t.Fatalf("marker %+v %v", m, err)
	}
	kept := filepath.Join(prev, update.PreviousSetupName)
	b, err := os.ReadFile(kept)
	if err != nil || string(b) != "setup program 1.2.0" {
		t.Fatalf("kept setup %q %v", b, err)
	}
	if sum, _ := fileSHA256(kept); sum != m.SetupSHA256 {
		t.Fatal("the marker hash is not the hash of the kept file")
	}
	// the working backup folders are gone as before; only .previous stays
	ents, _ := os.ReadDir(NewLayout(o, e.dirs).InstallDir)
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), ".setup-backup-") || strings.HasPrefix(en.Name(), ".setup-staging-") {
			t.Errorf("left behind: %s", en.Name())
		}
	}
}

func TestGoingBackClearsTheKeptVersion(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	e.upgradeTo("1.3.0")
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	prev := filepath.Join(NewLayout(o, e.dirs).InstallDir, update.PreviousDir)
	if !exists(prev) {
		t.Fatal("setup of the test")
	}
	e.upgradeTo("1.2.0") // the rollback: the old setup puts the old files back
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	if exists(prev) {
		t.Fatal("a downgrade left a marker that names the version it just left")
	}
}

func TestAFailedUpgradeLeavesNoKeptVersion(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	e.upgradeTo("1.3.0")
	e.r.FailAfter = func(id string) error {
		if id == "keep-previous" {
			return os.ErrPermission
		}
		return nil
	}
	if res := e.run(OpUpgrade, Options{}, UninstallOptions{}); res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s", res.Outcome)
	}
	if exists(filepath.Join(NewLayout(o, e.dirs).InstallDir, update.PreviousDir)) {
		t.Fatal("a rolled back run left the kept version")
	}
}

func TestRepairOfTheSameVersionKeepsNothing(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	e.mustRun(OpRepair, Options{}, UninstallOptions{})
	if exists(filepath.Join(NewLayout(o, e.dirs).InstallDir, update.PreviousDir)) {
		t.Fatal("repair kept a previous version")
	}
}
