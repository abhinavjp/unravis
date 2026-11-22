package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// askWait is how long the notice before the Windows prompt waits for an answer; no answer counts as Cancel.
const askWait = 10 * time.Minute

// recoverNotice is the box shown before the app is closed and before the Windows prompt (BL-183 b). It is the only thing that tells the
// person why a prompt is about to appear from a run they did not start.
const (
	recoverNoticeTitle = "Unravis"
	recoverNoticeText  = "An Unravis update was cut off before it finished.\n\nTo put things back, Windows will ask for administrator approval next. Click OK, then answer Yes.\n\nClick Cancel to leave it for now."
	recoverDeclinedMsg = "An update was cut off before it finished. It was not approved (Cancel, or no answer within 10 minutes), so it was not put back. Run Unravis Setup from the Unravis program folder and choose Repair."
)

// runRecover is --recover (BL-170): put back what a cut-off setup run changed, from its journal, and nothing else. A normal run replays
// the person's own journal; the machine journal (a folder only administrators can write) needs the Windows approval prompt, which is
// asked once, here, by starting this same program elevated, and only when that journal's folder and this program's folder can be changed
// by administrators alone (otherwise nothing is closed, asked or replayed). It installs nothing and never changes the version.
//
// The app that started this run is closed first and started again after: it lives in the folder this program sits in, which is also
// where the journal's undo of unravis-setup.exe puts the old file back (a file that is running is moved aside, not deleted).
func runRecover(o options, e env) int {
	r, d, err := newRunner(e)
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomePreflightFailed.ExitCode()
	}
	l, logPath := openLog(o, e, d.LocalAppData, false)
	defer closeLog(l)
	r.Log = l.Line
	say := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		fmt.Fprintln(e.stdout, msg)
		l.Line(msg)
	}
	l.Line("recover started by " + version.Short())
	admin := r.Sys.Mach.IsAdmin()
	old, _ := setup.Detect(r.Sys.Reg)
	// The app to close and start again is the one beside this program (the one that started it); the registry's install is the fallback.
	loc := ""
	if r.SetupExe != "" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(r.SetupExe), "unravis.exe")); err == nil {
			loc = filepath.Dir(r.SetupExe)
		}
	}
	if loc == "" && old != nil {
		loc = old.Location
	}
	pendingHere := r.JournalPending(admin)
	pendingMachine := false
	if !admin && r.JournalPending(true) {
		if err := r.MachineJournalTrusted(); err != nil {
			l.Line("recover: the machine journal is not used (its folder can be changed by others): " + err.Error())
		} else if err := r.SetupFolderTrusted(); err != nil {
			l.Line("recover: no Windows prompt is shown from a folder the person can change: " + err.Error())
		} else {
			pendingMachine = true
		}
	}
	if !pendingHere && !pendingMachine {
		say("Nothing to put back.")
		return 0
	}
	// Before anything is closed and before any prompt: a box that says what is about to happen. Cancel (or a closed box, or no answer)
	// leaves the machine journal and the running app alone. The elevated copy never asks, and a nil ask (tests) goes on.
	declined := false
	// --silent means no window at all, so it never shows the box and never waits for it: it behaves as it did before the box existed.
	ask := e.ask
	if o.silent {
		ask = nil
	}
	if pendingMachine && ask != nil {
		if !ask(recoverNoticeTitle, recoverNoticeText) {
			declined, pendingMachine = true, false
			l.Line("recover: not approved at the notice (Cancel, closed, or no answer); the machine journal is kept")
		}
	}
	res := setup.RecoverResult{}
	if pendingHere {
		res = r.Recover(admin, loc)
	}
	if res.Busy {
		say("Another Unravis setup is running; nothing was changed.")
		return setup.OutcomePreflightFailed.ExitCode()
	}
	code := 0
	var problems []string
	if pendingMachine {
		// The machine journal is replayed by an elevated copy of this program: it asks Windows for approval once.
		if loc != "" && !res.Closed {
			closed, busy := r.CloseApp(loc)
			if busy {
				say("Another Unravis setup is running; nothing was changed.")
				return setup.OutcomePreflightFailed.ExitCode()
			}
			res.Closed = closed
		}
		switch {
		case e.elevateWait == nil:
			problems = append(problems, "The administrator-level part could not be started.")
		default:
			c, err := e.elevateWait(r.SetupExe, []string{"--recover", "--no-restart"})
			if err != nil {
				l.Line("recover: the administrator part did not run: " + err.Error())
				problems = append(problems, "The administrator-level part did not run ("+err.Error()+").")
			} else if c != 0 {
				// The administrator part cannot tell this run why in words (its output is not shown). A refused journal is its usual reason,
				// so say which file it is and what can be done, without deleting anything.
				problems = append(problems, "The administrator-level part could not put everything back."+r.MachineJournalHelp())
			} else {
				res.Found = true
			}
		}
	}
	if res.Err != nil {
		l.Line("recover: " + res.Err.Error())
		problems = append(problems, res.Err.Error()+".")
	}
	for _, x := range res.Left {
		say("  left: %s", x)
	}
	if len(res.Left) > 0 {
		problems = append(problems, fmt.Sprintf("%d change(s) could not be put back.", len(res.Left)))
	}
	note := update.ApplyResult{At: e.now().UTC()}
	if old != nil {
		note.From = old.Version
	}
	if declined {
		note.Outcome = "failed"
		note.Message = recoverDeclinedMsg
		for _, p := range problems {
			note.Message += " " + p
		}
		code = setup.OutcomeCancelledUAC.ExitCode()
	} else if len(problems) == 0 && res.Found {
		note.Outcome = "rolled-back"
		note.Message = "An update was cut off before it finished. Unravis put back what it had changed."
	} else if len(problems) == 0 {
		say("Nothing needed putting back.")
	} else {
		note.Outcome = "failed"
		note.Message = "An update was cut off before it finished and could not be fully put back."
		for _, p := range problems {
			note.Message += " " + p
		}
		note.Message += " Run Unravis Setup from the Unravis program folder and choose Repair."
		code = setup.OutcomeRollbackLeft.ExitCode()
	}
	if note.Message != "" {
		l.Line("recover: result: " + note.Message) // the note is cut at 400 bytes; the log keeps all of it
	}
	// The elevated part never starts the app (it would run with administrator rights); the normal run that asked for it does.
	if res.Closed && loc != "" && !o.noRestart && e.startBackground != nil {
		if err := e.startBackground(filepath.Join(loc, "unravis.exe")); err != nil {
			l.Line("recover: could not start Unravis: " + err.Error())
			note.Message += " Unravis could not be started again; open it from the Start menu."
			note.Outcome = "failed"
		} else {
			note.Restarted = true
		}
	}
	if note.Outcome != "" && !o.noRestart && d.LocalAppData != "" {
		if err := update.WriteApplyResult(filepath.Join(d.LocalAppData, "Unravis", "update-result"), note); err != nil {
			l.Line("result: could not write the result: " + err.Error())
		}
	}
	say("recover: %s", note.Outcome)
	if logPath != "" {
		say("Log: %s", logPath)
	}
	return code
}
