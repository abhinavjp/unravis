package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"api-audit-explorer/internal/datadir"
)

// This file holds the checks that keep an elevated run from being steered by what a standard user can plant or own:
//
//   - links: a folder setup is about to change permissions on must be a real folder, not a junction to somewhere else (SetNamedSecurityInfo
//     follows a junction and would change the permissions of its target);
//   - owners: the journal folder under ProgramData is only used when administrators or the system own it;
//   - program folder: an install for everyone only takes over a folder this installer made;
//   - running programs: setup only closes the Unravis of the person who ran it.

// ErrAppOpenForOther is another person's Unravis running from the program folder. Setup never closes it: that person did not agree.
var ErrAppOpenForOther = &StepError{Msg: "Unravis is open for another person on this PC.",
	Fix: "Ask them to close Unravis (or sign out), then try again."}

func linkError(path string) error {
	return &StepError{Msg: path + " is a link to another place, not a real folder.",
		Fix: "Ask an administrator to remove that link, then run setup again."}
}

// refuseLink fails when path exists and is a link (a junction or a symbolic link). A path that is not there is fine.
func (r *Runner) refuseLink(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return linkError(path)
	}
	fs, err := r.Sys.Sec.Read(path)
	if errors.Is(err, datadir.ErrNotShared) {
		return nil // it went away since the Lstat
	}
	if err != nil {
		return &StepError{Msg: path + " could not be checked: " + plainError(err) + ".", Fix: "Restart the PC, then run setup again.", Err: err}
	}
	if fs.Reparse {
		return linkError(path)
	}
	return nil
}

func adminOwner(sid string) bool { return sid == "S-1-5-18" || sid == "S-1-5-32-544" }

// prepareJournalDir makes the journal folder. For an elevated run it is under ProgramData, where any user may create folders: setup
// makes it with its access list in the create call, and never takes over a folder that is a link or that the system or the
// administrators do not own.
func (r *Runner) prepareJournalDir(elevated bool) (string, error) {
	dir := r.journalDir(elevated)
	if !elevated {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := r.refuseLink(dir); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", err
		}
		if err := r.Sys.Sec.MkdirSDDL(dir, AdminOnlySDDL); err != nil {
			return "", err
		}
		return dir, nil
	} else if err != nil {
		return "", err
	}
	fs, err := r.Sys.Sec.Read(dir)
	if err != nil {
		return "", err
	}
	if !adminOwner(fs.Owner) {
		return "", &StepError{Msg: dir + " was made by another account, so setup will not keep its journal there.",
			Fix: "Ask an administrator to delete that folder, then run setup again."}
	}
	if err := r.Sys.Sec.SetSDDL(dir, AdminOnlySDDL); err != nil {
		return "", err
	}
	return dir, nil
}

// sameAccess reports whether the owner and access list read from a folder are the ones wantSDDL describes: the same owner, a list
// that is not null, and the same entries (type, flags, rights, account) in any order. An entry that came from the parent differs in
// its flags, so it counts as a difference.
func sameAccess(have datadir.FolderSecurity, wantSDDL string) bool {
	want, err := ParseSDDL(wantSDDL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(have.Owner, want.Owner) || have.NullDACL != want.NullDACL || len(have.ACEs) != len(want.ACEs) {
		return false
	}
	key := func(a datadir.FolderACE) string {
		return fmt.Sprintf("%d|%02x|%08x|%s", a.Type, a.Flags, a.Mask, strings.ToUpper(a.SID))
	}
	var h, w []string
	for _, a := range have.ACEs {
		h = append(h, key(a))
	}
	for _, a := range want.ACEs {
		w = append(w, key(a))
	}
	sort.Strings(h)
	sort.Strings(w)
	for i := range h {
		if h[i] != w[i] {
			return false
		}
	}
	return true
}

// programFolderProblem says why an existing program folder cannot be taken over by an install for everyone ("" when it can). It can
// be when this installer made it (its setup-state.json, or the install the machine's registry names), or when it is empty and only
// administrators own and change it. Anything else may be a person's own folder, and its permissions would be replaced.
func (r *Runner) programFolderProblem(dir string, old *Installed) (string, error) {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if st, err := ReadState(filepath.Join(dir, StateFileName)); err == nil && st.Scope == ScopeMachine && samePath(st.Location, dir) {
		return "", nil
	}
	if old != nil && old.Root == HKLM && samePath(old.Location, dir) {
		return "", nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(ents) > 0 {
		return dir + " already holds other files, and it was not made by the Unravis installer.", nil
	}
	fs, err := r.Sys.Sec.Read(dir)
	if err != nil {
		return "", err
	}
	if checkAdminOnly(dir, fs) != nil {
		return dir + " is already there, made by someone who is not an administrator.", nil
	}
	return "", nil
}

// splitApps divides the running Unravis programs into the ones of the person who ran setup and the ones of anyone else. A program
// whose owner the list did not report counts as the person's; one whose owner cannot be read counts as someone else's.
func (r *Runner) splitApps(running []Process) (mine, others []Process) {
	for _, q := range running {
		switch {
		case q.SID == "":
			mine = append(mine, q)
		case q.SID != UnknownOwner && r.UserSID != "" && strings.EqualFold(q.SID, r.UserSID):
			mine = append(mine, q)
		default:
			others = append(others, q)
		}
	}
	return mine, others
}

// CreateFileSDDL is the fake of FileSecurity.CreateFileSDDL: the file is made new (never over an existing one), written, and given
// its list at once.
func (f *FakeSecurity) CreateFileSDDL(path, sddl string, data []byte) error {
	if err := f.Faults.check("sec.CreateFileSDDL:" + path); err != nil {
		return err
	}
	if _, err := ParseSDDL(sddl); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		out.Close()
		os.Remove(path)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(path)
		return err
	}
	if err := f.SetSDDL(path, sddl); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// CreateFileSDDL makes a new file with its owner and access list already set, and journals it (the rollback deletes it). It never
// replaces a file that is there: that fails with os.ErrExist and leaves the old file alone.
func (t *Txn) CreateFileSDDL(path, sddl string, data []byte) error {
	if err := t.sys.Sec.CreateFileSDDL(path, sddl, data); err != nil {
		return err
	}
	if err := t.record(Undo{Kind: UndoRemoveFile, Path: path, Note: "file created: delete " + path}); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
