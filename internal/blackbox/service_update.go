package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReasonUpdateFailed: the recorder program could not be updated. The message
// says whether the previous program was put back and whether the recorder runs.
const ReasonUpdateFailed Reason = "update-failed"

// OldProgramSuffix is added to the installed program's name for the copy an
// update keeps of the previous program, so a failed start can be rolled back.
const OldProgramSuffix = ".old"

// updateSettle is how long the updated recorder must stay up before an update
// counts as good, when the status page cannot be asked.
const updateSettle = 2 * time.Second

// AliveProber is an optional part of Env: a check that the recorder answers on
// its status port WITHOUT a read key (an answer of 401 means it is up and is
// refusing a caller with no key). The elevated helper has no read key, only its
// hash is stored, so it cannot use ProbeStatus. An Env without it falls back to
// watching the service state for a moment.
type AliveProber interface {
	ProbeAlive(host string, port int) error
}

// UpdateError is an update that did not happen. Message is the plain sentence
// shown to the person; Reason is ReasonUpdateFailed.
type UpdateError struct {
	Reason  Reason
	Message string
	Cause   error
	// RolledBack is set when the previous program is in place again.
	RolledBack bool
	// Running says whether the recorder runs after the failure.
	Running bool
}

func (e *UpdateError) Error() string { return e.Message }
func (e *UpdateError) Unwrap() error { return e.Cause }

// UpdateResult is a finished update.
type UpdateResult struct {
	// Updated is false when the installed program already is the one Unravis
	// ships: nothing was stopped, copied or restarted.
	Updated bool
	// WasRunning says the recorder ran before the update; Restarted that it was
	// started again with the new program (a stopped recorder stays stopped).
	WasRunning bool
	Restarted  bool
	// OldHash and NewHash are the full SHA-256 of the program before and after.
	// OldHash is empty when the installed program was missing.
	OldHash string
	NewHash string
}

// ShortHash is the first 12 characters of a hash, for words and logs.
func ShortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func updateFailed(msg string, cause error, rolledBack, running bool) *UpdateError {
	return &UpdateError{Reason: ReasonUpdateFailed, Message: msg, Cause: cause, RolledBack: rolledBack, Running: running}
}

// stageProgram writes data to a new temporary file next to dst and returns its
// path. Nothing at dst changes. The file is made inside the program folder, so
// it takes that folder's locked permissions.
func stageProgram(dst string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".new-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

// Update replaces the installed recorder program with the one this process is
// (the Unravis-shipped program the helper was started from: the guard checked
// it before the prompt, exactly as for install). Only the program file changes:
// the settings, the read key hash and the recordings are never touched, and the
// program folder keeps the permissions Install gave it.
//
// A recorder that was running is stopped, the program is replaced atomically
// (a new file is written and checked beside it, then renamed over the old one;
// the previous program is kept as unravis-blackbox.exe.old), and the recorder is
// started again and checked. If anything fails after the program changed, the
// previous program is put back and started again. A recorder that was stopped
// stays stopped.
func (m *Manager) Update() (res UpdateResult, err error) {
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	st, serr := m.Svc.State(ServiceName)
	if serr != nil {
		return res, updateFailed("Could not ask Windows about the service, so nothing was changed: "+serr.Error(), serr, false, false)
	}
	if st == SvcNotInstalled {
		return res, ErrNotInstalled
	}
	progDir, perr := m.Env.ProgramDir()
	if perr != nil {
		return res, updateFailed("Windows did not say where the recorder is installed, so nothing was changed.", perr, false, st == SvcRunning)
	}
	exe := filepath.Join(progDir, InstalledExeName)
	old := exe + OldProgramSuffix
	src, xerr := m.Env.ExePath()
	if xerr != nil {
		return res, updateFailed("Unravis could not find the new recorder program, so nothing was changed.", xerr, false, st == SvcRunning)
	}
	data, rerr := os.ReadFile(src)
	if rerr != nil {
		return res, updateFailed("Unravis could not read the new recorder program, so nothing was changed. Antivirus may block it: allow it, then retry.", rerr, false, st == SvcRunning)
	}
	newSum := sha256Hex(data)
	res.NewHash = newSum
	oldSum := ""
	if _, statErr := os.Stat(exe); statErr == nil {
		var herr error
		if oldSum, herr = m.Env.FileSHA256(exe); herr != nil {
			return res, updateFailed("Unravis could not read the installed recorder, so nothing was changed.", herr, false, st == SvcRunning)
		}
	}
	res.OldHash = oldSum
	if samePath(src, exe) || (oldSum != "" && strings.EqualFold(oldSum, newSum)) {
		return res, nil // already current: nothing is stopped or restarted
	}

	// Where to ask once it runs again. Unreadable settings leave the status
	// check out; the recorder still has to stay running.
	host, port := "", 0
	if cfg, cerr := LoadConfig(installedPaths(progDir).ConfigPath); cerr == nil {
		host, port = ProbeHost(cfg.Bind), cfg.Port
	}

	wasRunning := st == SvcRunning || st == SvcStartPending || st == SvcStopPending
	res.WasRunning = wasRunning
	if st != SvcStopped {
		if st != SvcStopPending {
			if e := m.Svc.Stop(ServiceName); e != nil {
				return res, updateFailed("The recorder did not stop, so nothing was changed. It keeps running the previous program. Try again.", e, false, m.recoverService())
			}
		}
		if e := m.waitFor(SvcStopped, 30*time.Second); e != nil {
			return res, updateFailed("The recorder did not stop, so nothing was changed. It keeps running the previous program. Try again.", e, false, m.recoverService())
		}
	}

	// back puts the previous program in place again and starts it when it ran.
	// It runs only after the program file may have changed.
	back := func(cause error, msg string, haveOld bool) error {
		// The failed program may still run and hold its file: stop it first.
		if cur, _ := m.Svc.State(ServiceName); cur != SvcStopped && cur != SvcNotInstalled {
			_ = m.Svc.Stop(ServiceName)
			_ = m.waitFor(SvcStopped, 15*time.Second)
		}
		rolled := false
		if haveOld {
			if prev, e := os.ReadFile(old); e == nil {
				if e := writeFileAtomic(exe, prev, 0o755); e == nil {
					if got, e := m.Env.FileSHA256(exe); e == nil && strings.EqualFold(got, oldSum) {
						rolled = true
					}
				}
			}
		}
		running := false
		if wasRunning && rolled {
			running = m.recoverService()
		}
		switch {
		case !haveOld:
			msg += " The previous program could not be put back because there was no copy of it. Reinstall the Blackbox from Unravis."
		case !rolled:
			msg += " The previous program could not be put back. Remove the Blackbox and place it again."
		case wasRunning && running:
			msg += " The previous program was put back and the recorder is running with it again."
		case wasRunning:
			msg += " The previous program was put back, but the recorder is stopped. Press Start on the Blackbox, or check Services in Windows."
		default:
			msg += " The previous program was put back. The recorder was stopped and stays stopped."
		}
		return updateFailed(msg, cause, rolled, running)
	}

	// Stage and check the new program beside the old one, then keep a copy of the
	// old one and swap.
	staged, e := stageProgram(exe, data)
	if e != nil {
		return res, m.afterNoChange(wasRunning, updateFailed("The new recorder program could not be written, so nothing was changed. Antivirus may block it: allow unravis-blackbox.exe, then retry.", e, false, false))
	}
	defer os.Remove(staged) // gone after the rename; cleans up after any failure
	if got, e := m.Env.FileSHA256(staged); e != nil || !strings.EqualFold(got, newSum) {
		cause := e
		if cause == nil {
			cause = errors.New("the copy does not match the program")
		}
		return res, m.afterNoChange(wasRunning, updateFailed("The new recorder copy did not match, so nothing was changed. Antivirus may have changed it: allow unravis-blackbox.exe, then retry.", cause, false, false))
	}
	haveOld := false
	if oldSum != "" {
		prev, e := os.ReadFile(exe)
		if e == nil {
			e = writeFileAtomic(old, prev, 0o755)
		}
		if e != nil {
			return res, m.afterNoChange(wasRunning, updateFailed("Unravis could not keep a copy of the previous program, so nothing was changed.", e, false, false))
		}
		haveOld = true
	}
	if e := os.Rename(staged, exe); e != nil {
		return res, back(e, "The new recorder program could not be put in place.", haveOld)
	}
	if got, e := m.Env.FileSHA256(exe); e != nil || !strings.EqualFold(got, newSum) {
		cause := e
		if cause == nil {
			cause = errors.New("the installed program does not match the new program")
		}
		return res, back(cause, "The installed program did not match the new one after the copy.", haveOld)
	}

	if !wasRunning {
		res.Updated = true
		return res, nil
	}
	if e := m.Svc.Start(ServiceName); e != nil {
		return res, back(e, "The updated recorder did not start.", haveOld)
	}
	if e := m.waitFor(SvcRunning, 15*time.Second); e != nil {
		return res, back(e, "The updated recorder did not start.", haveOld)
	}
	if ap, ok := m.Env.(AliveProber); ok && port != 0 {
		if e := ap.ProbeAlive(host, port); e != nil {
			return res, back(e, "The updated recorder started but its status page did not answer.", haveOld)
		}
	} else {
		m.Env.Sleep(updateSettle)
	}
	if cur, e := m.Svc.State(ServiceName); e != nil || cur != SvcRunning {
		cause := e
		if cause == nil {
			cause = fmt.Errorf("the service is %s", cur)
		}
		return res, back(cause, "The updated recorder started but stopped again.", haveOld)
	}
	res.Updated, res.Restarted = true, true
	return res, nil
}

// afterNoChange starts the recorder again when it ran before and the program was
// never replaced, and says in the error whether it runs.
func (m *Manager) afterNoChange(wasRunning bool, ue *UpdateError) error {
	if wasRunning {
		ue.Running = m.recoverService()
		if ue.Running {
			ue.Message += " The recorder is running with the previous program."
		} else {
			ue.Message += " The recorder is stopped. Press Start on the Blackbox, or check Services in Windows."
		}
	} else {
		ue.Message += " The recorder was stopped and stays stopped."
	}
	return ue
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
