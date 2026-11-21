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

// JournalPending says whether a journal is on disk for this scope (it may still turn out to be committed; Recover deletes those).
func (r *Runner) JournalPending(elevated bool) bool {
	_, err := os.Stat(filepath.Join(r.journalDir(elevated), JournalName))
	return err == nil
}

// RecoverResult is what Recover did.
type RecoverResult struct {
	// Found says a journal of a run that did not finish was there and was replayed.
	Found bool
	// Left is what could not be put back.
	Left []string
	// Err is a journal that could not be used (a folder others can change, a file that cannot be read).
	Err error
	// Closed says Unravis was running from installDir and was closed first (it must be started again).
	Closed bool
}

// Recover takes the setup lock, closes Unravis when it runs from installDir (installDir "" means none is installed), and replays the
// journal of this scope. It is safe to call when there is nothing to do.
func (r *Runner) Recover(elevated bool, installDir string) RecoverResult {
	var res RecoverResult
	if !r.JournalPending(elevated) {
		return res
	}
	if r.Sys.Lock != nil {
		release, err := r.Sys.Lock()
		if err != nil {
			res.Err = errors.New("another Unravis setup is running")
			return res
		}
		defer release()
	}
	if installDir != "" {
		res.Closed = r.closeAppIn(installDir)
	}
	found, left, err := r.RecoverJournal(elevated)
	res.Found, res.Left, res.Err = found, left, err
	return res
}

// CloseApp asks Unravis in dir to quit and ends what is left (see closeAppIn); it reports whether any was running.
func (r *Runner) CloseApp(dir string) bool { return r.closeAppIn(dir) }

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
