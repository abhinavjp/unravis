package blackbox

import (
	"errors"
	"os"
	"path/filepath"
)

// The settings file sits in the program folder, which only administrators may read (SecureDir(progDir, false)). "Scan this PC" runs as
// the person, so it could see that the recorder was installed but not what it runs with (BL-166). The recorder therefore keeps a copy
// of its settings (name, folder, port, listening address and the rest of blackbox.json: no key, no secret) in a folder anyone may read
// and only administrators and SYSTEM may change. Install, Apply and every start of the service write it; Remove deletes it.

// PublicConfigName is the copy's file name.
const PublicConfigName = "this-pc.json"

// PublicConfigDir is where the copy lives: %ProgramData%\Unravis Blackbox. A variable so tests can point it at a temporary folder.
var PublicConfigDir = func() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		return ""
	}
	return filepath.Join(pd, "Unravis Blackbox")
}

// PublicConfigPath is the copy's full path ("" when the PC has no ProgramData folder).
func PublicConfigPath() string {
	dir := PublicConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, PublicConfigName)
}

// PublishThisPC writes the copy. The folder is locked first (administrators and SYSTEM write, everyone reads), so a person cannot put a
// file of their own there for the scan to trust. It is best effort for the caller: the recorder works without the copy.
func PublishThisPC(env interface {
	SecureDir(dir string, usersRead bool) error
}, cfg Config) error {
	dir := PublicConfigDir()
	if dir == "" {
		return errors.New("this PC has no ProgramData folder")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := env.SecureDir(dir, true); err != nil {
		return err
	}
	return SaveConfig(filepath.Join(dir, PublicConfigName), cfg, false)
}

// RemovePublicThisPC deletes the copy (and its folder when it is empty). Best effort.
func RemovePublicThisPC() {
	if p := PublicConfigPath(); p != "" {
		_ = os.Remove(p)
		_ = os.Remove(filepath.Dir(p))
	}
}
