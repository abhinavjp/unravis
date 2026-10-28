package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StepState is where a step is on the progress screen.
type StepState string

const (
	StepWaiting StepState = "waiting"
	StepRunning StepState = "running"
	StepDone    StepState = "done"
	StepFailed  StepState = "failed"
	StepUndone  StepState = "undone" // the rollback put back what it changed
	StepLeft    StepState = "left"   // the rollback could not put everything back
	StepSkipped StepState = "skipped"
)

// StepEvent is a step changing state.
type StepEvent struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	State  StepState `json:"state"`
	Detail string    `json:"detail,omitempty"`
}

// Outcome is how a run ended.
type Outcome string

const (
	OutcomeDone            Outcome = "done"
	OutcomeLeftovers       Outcome = "done-with-leftovers" // an uninstall that could not remove everything
	OutcomePreflightFailed Outcome = "preflight-failed"
	OutcomeInvalid         Outcome = "invalid"
	OutcomeCancelledUAC    Outcome = "cancelled-at-uac"
	OutcomeRolledBack      Outcome = "rolled-back"
	OutcomeRollbackLeft    Outcome = "rollback-incomplete"
	OutcomeCancelled       Outcome = "cancelled" // the person pressed Cancel; everything was put back
)

// ExitCode is the silent-mode exit code of an outcome (installer-and-releases.md, "Silent mode").
func (o Outcome) ExitCode() int {
	switch o {
	case OutcomeDone:
		return 0
	case OutcomePreflightFailed:
		return 1
	case OutcomeInvalid:
		return 2
	case OutcomeCancelledUAC:
		return 3
	case OutcomeRolledBack, OutcomeCancelled:
		return 4
	}
	return 5
}

// Result is the end of a run.
type Result struct {
	Outcome Outcome `json:"outcome"`
	Op      Op      `json:"op"`
	Message string  `json:"message"`
	// Failed is the step that failed and Fix what the person can do about it.
	Failed string   `json:"failed,omitempty"`
	Fix    string   `json:"fix,omitempty"`
	Left   []string `json:"left,omitempty"`
	// Checks are the preflight results when preflight stopped the run.
	Checks []Check `json:"checks,omitempty"`
	// ReadKey is the Blackbox read key. It goes to the setup window, which shows it once; it is never written to the log.
	ReadKey string `json:"readKey,omitempty"`
	Version string `json:"version,omitempty"`
	AppExe  string `json:"appExe,omitempty"`
	// RestartApp says Unravis was running before an upgrade and should be started again.
	RestartApp bool `json:"restartApp,omitempty"`
	// Kept lists the data an uninstall left in place on purpose.
	Kept []string `json:"kept,omitempty"`
}

// Step is one action of a plan.
type Step struct {
	ID    string
	Title string
	Do    func(ctx context.Context, t *Txn) error
}

// Runner runs plans against a System.
type Runner struct {
	Sys  *System
	Dirs Dirs
	// Payload is the program files to place; SetupExe is this setup program, copied into the program folder for repair and
	// uninstall ("" when it is already there).
	Payload  *Payload
	SetupExe string
	// UserSID is the person setup runs for (the owner of a silent-mode read key file).
	UserSID string
	// Log gets one line per action and per undo. It must never be given a secret.
	Log func(string)
	// Events gets every step change.
	Events func(StepEvent)
	// FailAfter is for tests: an error it returns after a step makes that step fail.
	FailAfter func(step string) error
	// BeforeStep is for tests: it runs before each step (a cancel in the middle of a run).
	BeforeStep func(step string)
	// BeforeCommit is for tests: it runs once every step is done, before the journal is retired.
	BeforeCommit func(j *Journal)
}

func (r *Runner) logf(format string, a ...any) {
	if r.Log != nil {
		r.Log(Redact(fmt.Sprintf(format, a...)))
	}
}

func (r *Runner) emit(e StepEvent) {
	if r.Events != nil {
		e.Detail = Redact(e.Detail)
		r.Events(e)
	}
}

// journalDir is where a run keeps its journal: under ProgramData (administrators only) when it runs elevated, in the person's folder
// otherwise.
func (r *Runner) journalDir(elevated bool) string {
	if elevated {
		return MachineJournalDir(r.Dirs)
	}
	return UserJournalDir(r.Dirs)
}

// ensureJournalDir makes the journal folder; the one under ProgramData is locked to administrators first, so nobody else can write a
// journal that the next elevated run would replay.
func (r *Runner) ensureJournalDir(elevated bool) (string, error) {
	dir := r.journalDir(elevated)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if elevated {
		if err := r.Sys.Sec.SetSDDL(dir, AdminOnlySDDL); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// RecoverJournal rolls back a journal left by a run that was cut off. It returns what could not be put back.
func (r *Runner) RecoverJournal(elevated bool) (found bool, left []string, err error) {
	p := filepath.Join(r.journalDir(elevated), JournalName)
	j, err := LoadJournal(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return true, nil, err
	}
	if elevated {
		// Only replay a journal in a folder nobody but administrators can write.
		if err := datadirCheck(r.Sys.Sec, filepath.Dir(p)); err != nil {
			return true, nil, fmt.Errorf("an earlier setup left a journal in a folder others can change, so it is not used: %v", err)
		}
	}
	if j.f.Committed {
		// That run finished; only its journal could not be deleted. Replaying it would undo a good install.
		r.logf("recover journal: the journal of an earlier %s that finished is deleted, not replayed", j.f.Op)
		if cerr := j.Close(); cerr != nil {
			r.logf("recover journal: could not delete it: %v", cerr)
		}
		return false, nil, nil
	}
	r.logf("recover journal: an earlier %s did not finish; putting back what it changed", j.f.Op)
	left = Rollback(r.Sys, j.Undos(), func(u Undo, err error) { r.logUndo(u, err) })
	if len(left) == 0 {
		_, _ = j.Commit(r.Sys.sleep) // fully undone: if it cannot be deleted it is marked so it is never replayed
	}
	return true, left, nil
}

func (r *Runner) logUndo(u Undo, err error) {
	if err != nil {
		r.logf("undo %s: %s: FAILED: %v", u.Step, u.Note, err)
	} else {
		r.logf("undo %s: %s: ok", u.Step, u.Note)
	}
}

// Execute runs a plan with a journal: on a failure or a cancel everything done is undone in reverse.
func (r *Runner) Execute(ctx context.Context, p *Plan) Result {
	res := Result{Op: p.Op, Version: p.Version, AppExe: p.Layout.AppExe}
	if r.Sys.Lock != nil {
		release, err := r.Sys.Lock()
		if err != nil {
			res.Outcome = OutcomePreflightFailed
			res.Message = "Another Unravis setup is already running. Finish or close it, then try again."
			r.logf("lock: FAILED: %v", err)
			return res
		}
		defer release()
	}
	if p.Op == OpUninstall {
		return r.uninstall(ctx, p, res)
	}
	elevated := p.Elevated
	if found, left, err := r.RecoverJournal(elevated); found && (err != nil || len(left) > 0) {
		res.Outcome = OutcomeRollbackLeft
		res.Message = "An earlier setup did not finish, and not everything it changed could be put back."
		res.Left = left
		if err != nil {
			res.Left = append(res.Left, err.Error())
		}
		return res
	}
	dir, err := r.ensureJournalDir(elevated)
	if err != nil {
		res.Outcome = OutcomeRolledBack
		res.Message = "Setup could not make its journal folder, so nothing was changed: " + err.Error()
		return res
	}
	j, err := NewJournal(filepath.Join(dir, JournalName), p.Op, p.Version, r.Sys.now())
	if err != nil {
		res.Outcome = OutcomeRolledBack
		res.Message = "Setup could not start its journal, so nothing was changed: " + err.Error()
		return res
	}
	r.logf("start %s %s (scope %s, folder %s)", p.Op, p.Version, p.Opts.Scope, p.Layout.InstallDir)
	var keep []string
	t := &Txn{sys: r.Sys, j: j, log: func(s string) { r.logf("%s", s) }, keep: &keep, backups: p.backupDir()}
	for _, s := range p.Steps {
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepWaiting})
	}
	var failed *Step
	var ferr error
	for i := range p.Steps {
		s := &p.Steps[i]
		if r.BeforeStep != nil {
			r.BeforeStep(s.ID)
		}
		if err := ctx.Err(); err != nil {
			failed, ferr = s, err
			break
		}
		t.step = s.ID
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepRunning})
		err := s.Do(ctx, t)
		if err == nil && r.FailAfter != nil {
			err = r.FailAfter(s.ID)
		}
		if err != nil {
			failed, ferr = s, err
			r.logf("%s: FAILED: %v", s.ID, err)
			break
		}
		r.logf("%s: ok", s.ID)
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepDone})
	}
	if failed == nil {
		// Commit: the journal is retired first and the backups go only after that, so a journal that is still on disk always has
		// what its rollback needs. If it cannot be retired the run counts as failed and is rolled back below, backups intact.
		if r.BeforeCommit != nil {
			r.BeforeCommit(j)
		}
		gone, cerr := j.Commit(r.Sys.sleep)
		if cerr != nil && len(p.Steps) > 0 {
			failed = &p.Steps[len(p.Steps)-1]
			ferr = &StepError{Msg: "Setup finished its work but could not close its journal in " + filepath.Dir(j.Path()),
				Fix: "Close programs that may hold files in that folder (antivirus, file sync), then run setup again.", Err: cerr}
			r.logf("commit: FAILED: %v", cerr)
		} else {
			if !gone {
				r.logf("commit: the journal %s could not be deleted; it is marked finished and the next run deletes it", j.Path())
			}
			for _, dir := range keep {
				if err := os.RemoveAll(dir); err != nil {
					r.logf("commit: could not remove working folder %s: %v", dir, err)
				}
			}
			res.Outcome = OutcomeDone
			res.ReadKey = p.readKey
			res.RestartApp = p.appWasRunning
			res.Message = doneMessage(p)
			r.logf("done %s %s", p.Op, p.Version)
			return res
		}
	}

	cancelled := errors.Is(ferr, context.Canceled)
	if cancelled {
		r.emit(StepEvent{ID: failed.ID, Title: failed.Title, State: StepFailed, Detail: "Cancelled"})
	} else {
		r.emit(StepEvent{ID: failed.ID, Title: failed.Title, State: StepFailed, Detail: plainError(ferr)})
	}
	stepLeft := map[string]bool{}
	left := Rollback(r.Sys, j.Undos(), func(u Undo, err error) {
		r.logUndo(u, err)
		if err != nil {
			stepLeft[u.Step] = true
		}
	})
	// Report each step that ran as undone (or left), newest first, the order the rollback took.
	ran := false
	for i := len(p.Steps) - 1; i >= 0; i-- {
		s := p.Steps[i]
		if s.ID == failed.ID {
			ran = true
		}
		if !ran {
			continue
		}
		st := StepUndone
		if stepLeft[s.ID] {
			st = StepLeft
		}
		if s.ID != failed.ID || stepLeft[s.ID] {
			r.emit(StepEvent{ID: s.ID, Title: s.Title, State: st})
		}
	}
	res.Failed = failed.Title
	res.Fix = fixFor(ferr)
	res.Left = left
	switch {
	case len(left) > 0:
		res.Outcome = OutcomeRollbackLeft
		res.Message = "Setup stopped at “" + failed.Title + "” and could not put everything back."
		r.logf("rollback incomplete: %d item(s) left; the journal is kept at %s", len(left), j.Path())
	case cancelled:
		_, _ = j.Commit(r.Sys.sleep) // fully undone: if it cannot be deleted it is marked so it is never replayed
		res.Outcome = OutcomeCancelled
		res.Message = "Cancelled. Everything setup changed has been put back."
		r.logf("cancelled; everything put back")
	default:
		_, _ = j.Commit(r.Sys.sleep) // fully undone: if it cannot be deleted it is marked so it is never replayed
		res.Outcome = OutcomeRolledBack
		res.Message = "Setup stopped at “" + failed.Title + "”: " + plainError(ferr) + " Everything it changed has been put back."
		r.logf("rolled back; everything put back")
	}
	if len(left) > 0 {
		// The journal stays only to keep the record; the next run would try the undos again, which may fail the same way, so it is
		// renamed out of the way and named in the log.
		_ = os.Rename(j.Path(), j.Path()+".failed-"+r.Sys.now().Format("20060102-150405"))
	}
	return res
}

func doneMessage(p *Plan) string {
	switch p.Op {
	case OpUpgrade:
		return "Unravis is upgraded to " + p.Version + ". Your cases and saved connections were kept."
	case OpRepair:
		return "Unravis is repaired."
	}
	return "Unravis " + p.Version + " is installed."
}

// plainError is the sentence of an error for the screen: the Problem or blackbox message as it is, else the error text.
func plainError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if s != "" && !strings.HasSuffix(s, ".") {
		s += "."
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// fixFor suggests what to do after a failure.
func fixFor(err error) string {
	var se *StepError
	if errors.As(err, &se) && se.Fix != "" {
		return se.Fix
	}
	if errors.Is(err, context.Canceled) {
		return "Run setup again when you are ready."
	}
	return "Read the setup log for the details, fix the cause, then run setup again."
}

// StepError is a step failure with a plain sentence and what to do.
type StepError struct {
	Msg string
	Fix string
	Err error
}

func (e *StepError) Error() string { return e.Msg }
func (e *StepError) Unwrap() error { return e.Err }

// datadirCheck refuses a folder whose permissions let someone other than administrators and the system change it.
func datadirCheck(sec FileSecurity, dir string) error {
	fs, err := sec.Read(dir)
	if err != nil {
		return err
	}
	return checkAdminOnly(dir, fs)
}
