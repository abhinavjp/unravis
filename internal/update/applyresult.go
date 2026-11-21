package update

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ApplyResult is what a silent, unattended update run leaves behind (docs/auto-updater/SILENT-UPDATE.md). Nobody watches that run, so
// the next time Unravis starts it reads this file and tells the person plainly what happened. It holds no secret; its message is setup's plain text (it can name a folder).
type ApplyResult struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Outcome   string    `json:"outcome"` // done, rolled-back, failed
	Message   string    `json:"message"` // plain words
	Restarted bool      `json:"restarted"`
	At        time.Time `json:"at"`
}

// ApplyResultName is the file inside the result folder.
const ApplyResultName = "last-update.json"

// WriteApplyResult writes r into dir through a temporary file, so a reader never sees half of it.
func WriteApplyResult(dir string, r ApplyResult) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if len(r.Message) > 400 {
		r.Message = r.Message[:400]
	}
	return writeJSON(filepath.Join(dir, ApplyResultName), r)
}

// ReadApplyResult reads the result in dir; (zero, false) when there is none or it is unreadable.
func ReadApplyResult(dir string) (ApplyResult, bool) {
	var r ApplyResult
	if err := readJSON(filepath.Join(dir, ApplyResultName), &r); err != nil {
		return ApplyResult{}, false
	}
	switch r.Outcome {
	case "done", "rolled-back", "failed":
		return r, true
	}
	return ApplyResult{}, false
}

// ClearApplyResult removes the result once the person has been told.
func ClearApplyResult(dir string) {
	if err := os.Remove(filepath.Join(dir, ApplyResultName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = err // best effort: the worst case is the notice shows twice
	}
}
