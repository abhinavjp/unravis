package setup

import (
	"errors"
	"fmt"
	"io"
	"math"
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
		if other := foreignEntries(ents); len(other) > 0 {
			return dir + " already holds files that are not Unravis files (" + namesLine(other) + "), and it was not made by the Unravis installer.", nil
		}
		return dir + " already holds Unravis files, but this installer did not make it as a folder for everyone.", nil
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
// whose owner the list did not report, or cannot be read, counts as someone else's: setup never closes a program it cannot show is the
// person's own.
func (r *Runner) splitApps(running []Process) (mine, others []Process) {
	for _, q := range running {
		switch {
		case q.SID != "" && q.SID != UnknownOwner && r.UserSID != "" && strings.EqualFold(q.SID, r.UserSID):
			mine = append(mine, q)
		default:
			others = append(others, q)
		}
	}
	return mine, others
}

// ReadFileProven is the fake of FileSecurity.ReadFileProven. The file must be a plain file (not a link of any kind) of at most max
// bytes; its list is its own when one was set, else the folder's (a file inherits, as on Windows); the owner must pass the owner rule
// (system or administrators only) and nobody else may write it. The fake cannot count hard links: that proof is the Windows one's.
func (f *FakeSecurity) ReadFileProven(path string, max int64) ([]byte, error) {
	if err := f.Faults.check("sec.ReadFileProven:" + path); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: it is not a plain file", ErrNotProven)
	}
	if max <= 0 || max == math.MaxInt64 {
		return nil, fmt.Errorf("%w: the size limit %d is not usable", ErrNotProven, max)
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%w: it is too large (%d bytes, at most %d)", ErrNotProven, fi.Size(), max)
	}
	f.mu.Lock()
	sddl, _, err := f.current(path)
	if _, own := f.m[strings.ToLower(filepath.Clean(path))]; !own && err == nil {
		sddl, _, err = f.current(filepath.Dir(path))
	}
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	fs, err := ParseSDDL(sddl)
	if err != nil {
		return nil, err
	}
	if !journalFileOwnerOK(fs.Owner) {
		return nil, fmt.Errorf("%w: it is owned by %s", ErrNotProven, fs.Owner)
	}
	if err := checkNoOthersWrite(path, fs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotProven, err)
	}
	in, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	data, err := io.ReadAll(io.LimitReader(in, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: it is too large (more than %d bytes)", ErrNotProven, max)
	}
	return data, nil
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
