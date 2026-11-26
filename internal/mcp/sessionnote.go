package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// When the dashboard cannot write its session file it keeps running (the window and the pipe do not need it), but a later `--mcp`
// would only say "Unravis is not running" with no hint why. So the dashboard leaves a short note, and `--mcp` reads it when the session
// file is missing and the dashboard that left it is still alive (RV-SEC-7, W-41).
//
// The note lives in the user's temporary folder, not beside the session file: the usual reason the session file cannot be written is
// that its folder cannot be written. It holds only the dashboard's process id and a fixed code, never an error text or a path, and
// `--mcp` turns the code into one of its own sentences, so a note written by someone else can only pick between those sentences.

// Reason codes of a session file that could not be written.
const (
	SessionFailAccess = "access" // the folder refused the write
	SessionFailFolder = "folder" // the folder could not be found or made
	SessionFailOther  = "other"  // anything else
)

type sessionNote struct {
	Pid  int    `json:"pid"`
	Code string `json:"code"`
}

// SessionFailureCode sorts the error of a failed session file write into one of the fixed codes.
func SessionFailureCode(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return SessionFailAccess
	case errors.Is(err, fs.ErrNotExist):
		return SessionFailFolder
	}
	return SessionFailOther
}

// SessionFailureText is the plain reason for a code, in words a first-time user can follow. An unknown code reads as "other".
func SessionFailureText(code string) string {
	switch code {
	case SessionFailAccess:
		return "Windows did not let Unravis write in its settings folder"
	case SessionFailFolder:
		return "Unravis could not find or make its settings folder"
	}
	return "something unexpected went wrong (the Unravis log has the details)"
}

// sessionNotePath names the note for one session file: the same session file path always gives the same note, so a dashboard started
// with another AUDIT_CONFIG_DIR does not touch this one's.
func sessionNotePath(sessionPath string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(sessionPath))))
	return filepath.Join(os.TempDir(), "unravis-session-note-"+hex.EncodeToString(sum[:6])+".json")
}

// RecordSessionFailure leaves the note for the session file at sessionPath. It is best effort: a note that cannot be written changes
// nothing else.
func RecordSessionFailure(sessionPath, code string) {
	if sessionPath == "" {
		return
	}
	data, err := json.Marshal(sessionNote{Pid: os.Getpid(), Code: code})
	if err != nil {
		return
	}
	temp, err := os.CreateTemp(os.TempDir(), ".unravis-session-note-*.tmp")
	if err != nil {
		return
	}
	name := temp.Name()
	_, werr := temp.Write(data)
	cerr := temp.Close()
	if werr != nil || cerr != nil || os.Rename(name, sessionNotePath(sessionPath)) != nil {
		_ = os.Remove(name)
	}
}

// ClearSessionFailure removes the note for the session file at sessionPath (a dashboard that wrote the file, or that is shutting down).
func ClearSessionFailure(sessionPath string) {
	if sessionPath == "" {
		return
	}
	if err := os.Remove(sessionNotePath(sessionPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = err // best effort: the worst case is a note that the liveness check below ignores
	}
}

// sessionFailureNote returns the plain reason when a live dashboard left a note for sessionPath. A note whose dashboard is gone is
// removed and ignored.
func sessionFailureNote(sessionPath string) (string, bool) {
	if sessionPath == "" {
		return "", false
	}
	path := sessionNotePath(sessionPath)
	f, err := os.Open(path) //nolint:gosec // the name is built here from a hash of the session path, inside the temporary folder
	if err != nil {
		return "", false
	}
	data, rerr := io.ReadAll(io.LimitReader(f, 1<<10))
	_ = f.Close()
	var n sessionNote
	if rerr != nil || json.Unmarshal(data, &n) != nil || n.Pid <= 0 {
		return "", false
	}
	if !processAlive(n.Pid) {
		_ = os.Remove(path)
		return "", false
	}
	return SessionFailureText(n.Code), true
}

// sessionNotWritten is the message `--mcp` gives when a running dashboard could not write its session file.
func sessionNotWritten(reason string) string {
	return "Unravis is running, but its session file could not be written: " + reason +
		". Fix that and restart Unravis, or start --mcp with --port and AUDIT_SESSION_TOKEN"
}
