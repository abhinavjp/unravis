//go:build windows

package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The package's tests must never write to the real %ProgramData%; each test that wants the copy points it at a temporary folder.
func TestMain(m *testing.M) {
	PublicConfigDir = func() string { return "" }
	os.Exit(m.Run())
}

type secEnv struct {
	err  error
	dirs []string
}

func (e *secEnv) SecureDir(dir string, usersRead bool) error {
	e.dirs = append(e.dirs, dir)
	if !usersRead {
		return errors.New("the copy's folder must stay readable")
	}
	return e.err
}

func pointPublicDirAt(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Unravis Blackbox")
	old := PublicConfigDir
	PublicConfigDir = func() string { return dir }
	t.Cleanup(func() { PublicConfigDir = old })
	return dir
}

func TestPublishedCopyLetsScanReadWhatTheProgramFolderHides(t *testing.T) {
	dir := pointPublicDirAt(t)
	cfg := packCfg()
	cfg.Port = 7803
	env := &secEnv{}
	if err := PublishThisPC(env, cfg); err != nil {
		t.Fatal(err)
	}
	if len(env.dirs) != 1 || env.dirs[0] != dir {
		t.Fatalf("the folder must be locked before the file is written: %v", env.dirs)
	}
	denied := func(path string) (Config, error) {
		if filepath.Base(path) == ConfigFileName {
			return Config{}, errors.New("access denied")
		}
		return LoadConfig(path)
	}
	f, err := ScanThisPC(scanSvc{st: SvcRunning}, scanEnv{dir: `C:\PF\Unravis Blackbox`}, denied)
	if err != nil || f.ConfigUnreadable || f.Name != cfg.Name || f.Port != 7803 || f.Folder != cfg.Folder || f.Settings == nil {
		t.Fatalf("%+v %v", f, err)
	}
	RemovePublicThisPC()
	if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("the copy and its empty folder must go: %v", serr)
	}
	f, _ = ScanThisPC(scanSvc{st: SvcRunning}, scanEnv{dir: "x"}, denied)
	if !f.ConfigUnreadable {
		t.Fatal("with no copy the settings are unreadable, as before")
	}
}

func TestNoCopyIsWrittenIntoAFolderThatCouldNotBeLocked(t *testing.T) {
	dir := pointPublicDirAt(t)
	if err := PublishThisPC(&secEnv{err: errors.New("acl")}, packCfg()); err == nil {
		t.Fatal("an unlockable folder must stop the write")
	}
	if _, err := os.Stat(filepath.Join(dir, PublicConfigName)); err == nil {
		t.Fatal("a file in a folder others could change must not be written")
	}
}

func TestApplyKeepsTheCopyCurrent(t *testing.T) {
	// Install and Apply call PublishThisPC through the Manager's Env; the call sites are covered by the build and by this guard on the
	// file name the scan reads.
	if PublicConfigName != "this-pc.json" {
		t.Fatal("the name is part of what older and newer programs agree on")
	}
}
