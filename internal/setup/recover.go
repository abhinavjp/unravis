package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BL-170, the no-touch half. A setup run that was cut off (power loss, a killed process) leaves its journal. Every setup run replays it
// first, but nobody may start one, so the app starts `unravis-setup.exe --recover` from its own program folder. That run does nothing
// but put back what the journal lists. The journal folder decides whose journal it is: an elevated run reads only the machine journal
// (a folder only administrators can write, checked as always), a normal run only the person's own. Nothing is read from the network and
// nothing is installed: it never takes the app to another version.

// JournalPending says whether a journal of a run that did not finish is on disk for this scope. A journal marked committed (the run
// finished and only its deletion failed) is not pending: it is never replayed, so nothing needs the app closed or a prompt shown. A
// journal that was found but refused (not proven, cannot be read) counts as pending: the replay reports it and keeps it. It goes
// through readJournal, the one place a journal is read, so the check cannot read a byte the replay would not.
func (r *Runner) JournalPending(elevated bool) bool {
	j, found, err := r.readJournal(elevated)
	if !found {
		return false
	}
	return err != nil || !j.f.Committed
}

// readJournal is the one way a journal is read. found is false only when there is no journal file. err != nil with found == true
// says a journal is there and was refused (a link, a folder others can change, a file that is not proven, too large, not JSON) or
// could not be read: the file is left alone, never deleted, never replayed. Any error from the reader other than "not there" is a
// refusal, whatever its kind (a sharing violation and access denied are refusals too).
//
// Not elevated: the person's own file, read by path as the person. Elevated: no byte of the file is read until every folder from
// ProgramData down to the journal folder is a real folder, the journal folder is one only administrators can change, and the file
// itself is proven on the handle it is read from (FileSecurity.ReadFileProven: no link, one name, owner and list, at most 8 MiB).
func (r *Runner) readJournal(elevated bool) (j *Journal, found bool, err error) {
	p := filepath.Join(r.journalDir(elevated), JournalName)
	if !elevated {
		if _, serr := os.Lstat(p); errors.Is(serr, os.ErrNotExist) {
			return nil, false, nil
		}
		j, err = LoadJournal(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return j, true, err
	}
	dir := filepath.Dir(p)
	// The folders first, so a link in the way is a refusal and never "no journal": a junction in the middle that points at a folder
	// without the file would otherwise look like an absent file.
	for _, d := range []string{r.Dirs.ProgramData, dir} {
		fi, lerr := os.Lstat(d)
		switch {
		case errors.Is(lerr, os.ErrNotExist):
			return nil, false, nil
		case lerr != nil:
			return nil, true, fmt.Errorf("an earlier setup may have left a journal in a folder that cannot be looked at, so it is not used: %v.%s", lerr, journalFolderAdvice(dir))
		case fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
			return nil, true, fmt.Errorf("an earlier setup left a journal in a folder that is a link, so it is not used.%s", journalFolderAdvice(dir))
		}
	}
	// Whether a file is there is metadata only: a folder nobody trusts and that holds no journal is not a pending journal.
	if _, lerr := os.Lstat(p); errors.Is(lerr, os.ErrNotExist) {
		return nil, false, nil
	} else if lerr != nil {
		return nil, true, fmt.Errorf("an earlier setup left a journal that cannot be looked at, so it is not used: %v.%s", lerr, journalFileAdvice(p))
	}
	if cerr := datadirCheck(r.Sys.Sec, dir); cerr != nil {
		return nil, true, fmt.Errorf("an earlier setup left a journal in a folder others can change, so it is not used: %v.%s", cerr, journalFolderAdvice(dir))
	}
	data, rerr := r.Sys.Sec.ReadFileProven(p, maxJournalBytes)
	if errors.Is(rerr, os.ErrNotExist) {
		return nil, false, nil // gone between the look and the open
	}
	if errors.Is(rerr, ErrNotProven) {
		return nil, true, fmt.Errorf("an earlier setup left a journal that is not a plain file only administrators made, so it is not used: %v.%s", rerr, journalFileAdvice(p))
	}
	if rerr != nil {
		return nil, true, fmt.Errorf("an earlier setup left a journal that could not be opened safely, so it is not used: %v.%s", rerr, journalFileAdvice(p))
	}
	j, err = ParseJournal(data, p)
	if err != nil {
		return nil, true, fmt.Errorf("%w.%s", err, journalFileAdvice(p))
	}
	// A journal marked committed by the replay is written again: with the same owner, so it is still one that can be read.
	j.w = &journalWriter{sec: r.Sys.Sec, sddl: MachineJournalFileSDDL}
	return j, true, nil
}

// journalFileAdvice and journalFolderAdvice end every refusal of the machine journal. A refused journal is kept and blocks every elevated
// setup until somebody deals with it, so the person is told which file it is, that nothing was deleted, and what they can do: move it
// (moving keeps it, deleting does not), then run setup again. Neither action deletes anything.
func journalFileAdvice(p string) string {
	return " The journal is the file " + p + ". It was not used and it was not deleted. To go on: open it in Notepad to see what it lists," +
		" then, while no Unravis setup is running, move the file out of its folder (for example to your Desktop; do not delete it)." +
		" Then run setup again. If the earlier run left things half done, choose Repair."
}

func journalFolderAdvice(dir string) string {
	return " The folder is " + dir + ". Nothing in it was used and nothing was deleted. To go on: an administrator makes sure only SYSTEM and" +
		" Administrators can change that folder, or, while no Unravis setup is running, renames the folder (for example to \"Unravis Setup old\";" +
		" do not delete it). Then run setup again; it makes a new one. If the earlier run left things half done, choose Repair."
}

// journalFileOwnerOK says whether owner may own the machine journal file: only the system or the administrators group. The account
// that runs the elevated copy is deliberately not accepted. A normal administrator runs an elevated and an unelevated process under
// the same account, and the owner of a file always holds the right to rewrite its access list (WRITE_DAC), which no entry in the list
// shows: an unelevated program could open the file, grant itself write, change the undo list and put the list back, and the file
// would still look administrators-only. A group the unelevated token holds only as "deny only" (Administrators) has no such right.
// A journal that the elevated account itself owns is therefore refused (and kept). Setup's own writer therefore makes the machine
// journal with owner Administrators (Journal.save with MachineJournalFileSDDL), and a Windows test reads back what the real writer
// wrote; it does not rely on the default owner of the token, which is not always that group.
func journalFileOwnerOK(owner string) bool {
	return owner == "S-1-5-18" || owner == "S-1-5-32-544"
}

// MachineJournalTrusted says whether the machine journal's folder is one only administrators can change, which is the condition for
// replaying it as an administrator. A normal run asks it before it shows a Windows prompt for that journal.
func (r *Runner) MachineJournalTrusted() error {
	return datadirCheck(r.Sys.Sec, r.journalDir(true))
}

// SetupFolderTrusted says whether the folder this setup program runs from can be changed only by administrators. A normal run must not
// start itself elevated from a folder the person can write: any program of theirs could have replaced the file.
func (r *Runner) SetupFolderTrusted() error {
	if r.SetupExe == "" {
		return errors.New("the setup program's folder is not known")
	}
	return datadirCheck(r.Sys.Sec, filepath.Dir(r.SetupExe))
}

// RecoverResult is what Recover did.
type RecoverResult struct {
	// Found says a journal of a run that did not finish was there and was replayed.
	Found bool
	// Left is what could not be put back.
	Left []string
	// Err is a journal that could not be used (a folder others can change, a file that cannot be read) or the setup lock being held.
	Err error
	// Busy says another setup holds the lock: nothing was touched and the app was not closed.
	Busy bool
	// Closed says Unravis was running from installDir and was closed first (it must be started again).
	Closed bool
}

// withLock runs f while holding the one-setup-at-a-time lock. ok is false when another setup holds it (f did not run).
func (r *Runner) withLock(f func()) (ok bool) {
	if r.Sys.Lock != nil {
		release, err := r.Sys.Lock()
		if err != nil {
			return false
		}
		defer release()
	}
	f()
	return true
}

// Recover takes the setup lock, closes Unravis when it runs from installDir (installDir "" means none is installed), and replays the
// journal of this scope. It is safe to call when there is nothing to do: nothing is closed unless a journal is really pending.
func (r *Runner) Recover(elevated bool, installDir string) RecoverResult {
	var res RecoverResult
	if !r.JournalPending(elevated) {
		return res
	}
	if !r.withLock(func() {
		if installDir != "" {
			res.Closed = r.closeAppIn(installDir)
		}
		res.Found, res.Left, res.Err = r.RecoverJournal(elevated)
	}) {
		res.Busy, res.Err = true, errors.New("another Unravis setup is running")
	}
	return res
}

// CloseApp closes Unravis in dir under the setup lock and reports whether any was running (busy: the lock was held, nothing closed).
func (r *Runner) CloseApp(dir string) (closed, busy bool) {
	if !r.withLock(func() { closed = r.closeAppIn(dir) }) {
		return false, true
	}
	return closed, false
}

// closeAppIn asks Unravis in dir to quit, and ends what is left after a short wait. It reports whether any was running.
func (r *Runner) closeAppIn(dir string) bool {
	list, err := r.Sys.Proc.List()
	if err != nil {
		return false
	}
	var running []Process
	for _, q := range list {
		if strings.EqualFold(filepath.Base(q.Exe), AppExeName) && insideFolder(q.Exe, dir) {
			running = append(running, q)
		}
	}
	mine, _ := r.splitApps(running)
	if len(mine) == 0 {
		return false
	}
	left := mine
	if r.Sys.AskQuit != nil {
		if err := r.Sys.AskQuit(); err == nil {
			for round := 0; round < askQuitRounds && len(left) > 0; round++ {
				r.Sys.sleep(askQuitPause)
				now, err := r.Sys.Proc.List()
				if err != nil {
					break
				}
				var again []Process
				for _, q := range now {
					if strings.EqualFold(filepath.Base(q.Exe), AppExeName) && insideFolder(q.Exe, dir) {
						again = append(again, q)
					}
				}
				latestMine, _ := r.splitApps(again)
				left = stillRunning(mine, latestMine)
			}
		}
	}
	for _, q := range left {
		r.logf("recover: closing Unravis (process %d)", q.PID)
		_ = r.Sys.Proc.TerminateIf(q.PID, q.Exe)
	}
	return true
}

// MachineJournalHelp is what a person can do when the administrator part of a recovery failed because it refused the machine journal. The
// normal run cannot look inside that folder (administrators only) and the administrator part's output is not shown, so this names the file
// and says that moving it keeps it (nothing is deleted). It is worded as a condition: the same failure can have another cause. It is short
// on purpose: the result note that carries it is cut at 400 bytes, so the advice comes first and the path last.
func (r *Runner) MachineJournalHelp() string {
	return " If it refused the journal, move that file out of its folder (do not delete it) and run setup again: " +
		filepath.Join(r.journalDir(true), JournalName) + "."
}
