package localci

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// PauseFileName is the file in the state folder whose presence stops an agent from taking new requests. What
// already runs keeps running. A person, a script that needs the PC quiet, or a queue-pause command makes it;
// the agent looks at it before every claim (see Agent.Paused).
const PauseFileName = "PAUSE"

// PausePath is the pause file of a state folder.
func PausePath(stateDir string) string { return filepath.Join(stateDir, PauseFileName) }

// IsPaused says whether the pause file of the state folder exists.
func IsPaused(stateDir string) bool {
	_, err := os.Stat(PausePath(stateDir))
	return err == nil
}

// SetPaused makes (true) or removes (false) the pause file. Both are safe to repeat: pausing a paused folder or
// resuming one that is not paused changes nothing and is not an error.
func SetPaused(stateDir string, paused bool) error {
	p := PausePath(stateDir)
	if !paused {
		if err := retryFS(func() error { return os.Remove(p) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: p is the PAUSE file of the store folder
	if errors.Is(err, fs.ErrExist) {
		return nil // already paused (maybe by hand): leave the file as it is
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteString("paused by a queue pause; remove this file or resume the queue\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
