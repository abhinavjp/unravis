package setup

import (
	"errors"
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
// finished and only its deletion failed) is not pending: it is never replayed, so nothing needs the app closed or a prompt shown.
func (r *Runner) JournalPending(elevated bool) bool {
	p := filepath.Join(r.journalDir(elevated), JournalName)
	if _, err := os.Stat(p); err != nil {
		return false
	}
	j, err := LoadJournal(p)
	return err != nil || !j.f.Committed // one that cannot be read is dealt with (and reported) by the replay
}

// journalFileOwnerOK says whether owner may own the machine journal file: only the system or the administrators group. The account
// that runs the elevated copy is deliberately not accepted. A normal administrator runs an elevated and an unelevated process under
// the same account, and the owner of a file always holds the right to rewrite its access list (WRITE_DAC), which no entry in the list
// shows: an unelevated program could open the file, grant itself write, change the undo list and put the list back, and the file
// would still look administrators-only. A group the unelevated token holds only as "deny only" (Administrators) has no such right.
// A journal that the elevated account itself owns is therefore refused (and kept); setup writes it so that it is not.
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
