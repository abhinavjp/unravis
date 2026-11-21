package main

import (
	"fmt"
	"path/filepath"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// runRecover is --recover (BL-170): put back what a cut-off setup run changed, from its journal, and nothing else. A normal run replays
// the person's own journal; the machine journal (a folder only administrators can write) needs the Windows approval prompt, which is
// asked once, here, by starting this same program elevated. It installs nothing and never changes the version.
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
	loc := ""
	if old != nil {
		loc = old.Location
	}
	pendingHere := r.JournalPending(admin)
	pendingMachine := !admin && r.JournalPending(true)
	if !pendingHere && !pendingMachine {
		say("Nothing to put back.")
		return 0
	}
	res := setup.RecoverResult{}
	if pendingHere {
		res = r.Recover(admin, loc)
	}
	code := 0
	extra := ""
	if pendingMachine {
		// The machine journal is replayed by an elevated copy of this program: it asks Windows for approval once.
		if loc != "" && !res.Closed {
			res.Closed = r.CloseApp(loc)
		}
		if e.elevateWait == nil {
			extra = " A cut-off administrator-level update remains; run Unravis Setup and choose Repair."
			code = setup.OutcomeRollbackLeft.ExitCode()
		} else if c, err := e.elevateWait(r.SetupExe, []string{"--recover"}); err != nil {
			l.Line("recover: the administrator part did not run: " + err.Error())
			extra = " The administrator part did not run (" + err.Error() + "); run Unravis Setup and choose Repair."
			code = setup.OutcomeRollbackLeft.ExitCode()
		} else if c != 0 {
			extra = " The administrator part could not put everything back; run Unravis Setup and choose Repair."
			code = c
		} else {
			res.Found = true
		}
	}
	if res.Err != nil {
		extra += " " + res.Err.Error() + "."
		code = setup.OutcomeRollbackLeft.ExitCode()
	}
	if len(res.Left) > 0 {
		for _, x := range res.Left {
			say("  left: %s", x)
		}
		code = setup.OutcomeRollbackLeft.ExitCode()
		extra += " Some changes could not be put back; run Unravis Setup and choose Repair."
	}
	note := update.ApplyResult{At: e.now().UTC(), Outcome: "rolled-back",
		Message: "An update was cut off before it finished. Unravis put back what it had changed." + extra}
	if code != 0 {
		note.Outcome = "failed"
	}
	if old != nil {
		note.From = old.Version
	}
	if res.Closed && loc != "" && e.startBackground != nil {
		if err := e.startBackground(filepath.Join(loc, "unravis.exe")); err != nil {
			l.Line("recover: could not start Unravis: " + err.Error())
			note.Message += " Unravis could not be started again; open it from the Start menu."
			note.Outcome = "failed"
		} else {
			note.Restarted = true
		}
	}
	if d.LocalAppData != "" {
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
