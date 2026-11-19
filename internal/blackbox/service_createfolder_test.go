package blackbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// creatorEnv is a fake machine that can make a folder with its permissions set in
// the create call, and can be told that the name appeared first.
type creatorEnv struct {
	*fakeEnv
	made   []string
	raceTo error // returned instead of making the folder
	secure []bool
}

func (e *creatorEnv) CreateSecureDir(dir string, usersRead bool) error {
	if e.raceTo != nil {
		return e.raceTo
	}
	e.made = append(e.made, dir)
	e.secure = append(e.secure, usersRead)
	return os.Mkdir(dir, 0o700)
}

// SEC-FIX-06: the recordings folder itself is made through the secure create,
// the folders above it are plain, and undo removes them deepest first.
func TestCreateFolderMakesTheLeafSecurelyAndUndoesIt(t *testing.T) {
	g := newSvcRig(t)
	ce := &creatorEnv{fakeEnv: g.env}
	g.m.Env = ce
	var undo undoList
	if err := g.m.createFolder(g.cfg.Folder, true, &undo); err != nil {
		t.Fatal(err)
	}
	if len(ce.made) != 1 || ce.made[0] != filepath.Clean(g.cfg.Folder) || !ce.secure[0] {
		t.Fatalf("the secure create was used for %v", ce.made)
	}
	if st, err := os.Stat(g.cfg.Folder); err != nil || !st.IsDir() {
		t.Fatal("folder not made")
	}
	undo.run()
	if _, err := os.Stat(filepath.Dir(g.cfg.Folder)); err == nil {
		t.Fatal("the folders it made were left behind")
	}
}

// An existing folder is left alone: SecureDir judges it afterwards.
func TestCreateFolderLeavesAnExistingFolder(t *testing.T) {
	g := newSvcRig(t)
	ce := &creatorEnv{fakeEnv: g.env}
	g.m.Env = ce
	if err := os.MkdirAll(g.cfg.Folder, 0o755); err != nil {
		t.Fatal(err)
	}
	var undo undoList
	if err := g.m.createFolder(g.cfg.Folder, true, &undo); err != nil {
		t.Fatal(err)
	}
	undo.run()
	if len(ce.made) != 0 {
		t.Fatalf("an existing folder was remade: %v", ce.made)
	}
	if _, err := os.Stat(g.cfg.Folder); err != nil {
		t.Fatal("undo removed a folder it did not make")
	}
}

// SEC-FIX-05/06: a name that appears between the check and the create is refused.
func TestInstallRefusesAFolderThatAppearedFirst(t *testing.T) {
	g := newSvcRig(t)
	g.m.Env = &creatorEnv{fakeEnv: g.env, raceTo: &fs.PathError{Op: "mkdir", Path: g.cfg.Folder, Err: fs.ErrExist}}
	_, err := g.m.Install(g.cfg)
	if err == nil {
		t.Fatal("install should fail")
	}
	if r := installReason(t, err); r != ReasonFolderUnsafe {
		t.Fatalf("reason %s: %v", r, err)
	}
	var ie *InstallError
	if !errors.As(err, &ie) || !errors.Is(ie.Cause, ErrFolderChanged) {
		t.Fatalf("cause %v", err)
	}
	g.nothingLeft(t)
}

// ... and SecureDir refusing a folder that changed maps to the same reason.
func TestInstallMapsAChangedFolderFromSecureDir(t *testing.T) {
	g := newSvcRig(t)
	g.env.secureErr = ErrFolderChanged
	_, err := g.m.Install(g.cfg)
	if err == nil {
		t.Fatal("install should fail")
	}
	if r := installReason(t, err); r != ReasonFolderUnsafe {
		t.Fatalf("reason %s: %v", r, err)
	}
	g.nothingLeft(t)
}
