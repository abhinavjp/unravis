// Package releasehold keeps the holds (freeze windows) of the release flow. A hold names a project, or all of them, a
// reason, an owner and a time window. While a hold is active, Promote is refused with that reason; release cutting and CI
// runs are not held.
//
// The holds live in one versioned JSON file in the CI store folder, beside the queue (holds.json). Writers take a lock
// file, read, change and write the file atomically (a temp file renamed over it), so two commands at once never lose a
// hold. Readers are tolerant of fields they do not know, but never of a file they cannot read: a corrupt file or one
// from a newer version is an error, so a check that cannot read the holds refuses instead of passing.
//
// The package has no UI or network dependency and reads the clock only through the now arguments.
package releasehold

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// FileName is the hold store file in the CI store folder.
const FileName = "holds.json"

// Version is the store format this package writes and the highest it reads.
const Version = 1

// AllProjects is the project value of a hold that holds every project.
const AllProjects = "all"

// Limits on the text a hold carries. Longer text is refused, not cut, so nobody's reason is changed behind their back.
const (
	MaxReason = 300
	MaxPerson = 80
)

// States of a hold at a moment (see Hold.StateAt).
const (
	StateUpcoming = "upcoming" // the window has not started yet
	StateActive   = "active"   // Promote is refused
	StateEnded    = "ended"    // the window is over
	StateLifted   = "lifted"   // somebody ended it early
)

// Hold is one freeze window. The JSON names are part of the store format and of the CLI's --json output: add fields, do
// not rename or drop them.
type Hold struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"` // a project id, or "all"
	Reason     string    `json:"reason"`
	Owner      string    `json:"owner"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"` // the hold is active up to, not including, this moment
	Created    time.Time `json:"created"`
	LiftedBy   string    `json:"liftedBy,omitempty"`
	LiftedAt   time.Time `json:"liftedAt,omitzero"`
	LiftReason string    `json:"liftReason,omitempty"`
}

// Lifted says whether somebody ended the hold early.
func (h Hold) Lifted() bool { return !h.LiftedAt.IsZero() }

// StateAt is the state of the hold at now.
func (h Hold) StateAt(now time.Time) string {
	switch {
	case h.Lifted() && !now.Before(h.LiftedAt):
		return StateLifted
	case now.Before(h.Start):
		return StateUpcoming
	case now.Before(h.End):
		return StateActive
	}
	return StateEnded
}

// ActiveAt says whether the hold blocks Promote at now.
func (h Hold) ActiveAt(now time.Time) bool { return h.StateAt(now) == StateActive }

// Covers says whether the hold applies to project. An "all" hold covers every project. An empty project means "any
// project": every hold covers it.
func (h Hold) Covers(project string) bool {
	return project == "" || h.Project == AllProjects || h.Project == project
}

type file struct {
	Version int    `json:"version"`
	Holds   []Hold `json:"holds"`
}

// Store is the hold store of one CI store folder.
type Store struct {
	Dir string // the CI store folder; the file is Dir/holds.json
}

// Path is the store file.
func (s Store) Path() string { return filepath.Join(s.Dir, FileName) }

// ErrNotFound is returned by Lift for an id that is not in the store.
var ErrNotFound = errors.New("no hold with that id")

// ErrStoreBroken is matched (errors.Is) by the error of a holds.json that is not JSON, has the wrong types, an entry
// with no id or end, or a newer format version: the store is broken, the request is not wrong.
var ErrStoreBroken = errors.New("the hold store is broken")

type brokenStore struct{ err error }

func (e brokenStore) Error() string        { return e.err.Error() }
func (e brokenStore) Unwrap() error        { return e.err }
func (e brokenStore) Is(target error) bool { return target == ErrStoreBroken }

// List reads every hold, oldest first. A store folder without the file has no holds. A file that cannot be read or
// parsed, or one written by a newer version, is an error: the caller must treat that as "held" (fail closed).
func (s Store) List() ([]Hold, error) {
	var b []byte
	err := retryFS(func() (e error) { b, e = os.ReadFile(s.Path()); return e })
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the hold store %s: %w", s.Path(), err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, brokenStore{fmt.Errorf("the hold store %s is not valid JSON: %w", s.Path(), err)}
	}
	if f.Version < 1 || f.Version > Version {
		return nil, brokenStore{fmt.Errorf("the hold store %s has format version %d; this program reads version 1 to %d", s.Path(), f.Version, Version)}
	}
	for i, h := range f.Holds {
		if h.ID == "" || h.End.IsZero() {
			return nil, brokenStore{fmt.Errorf("the hold store %s: entry %d has no id or no end", s.Path(), i+1)}
		}
	}
	return f.Holds, nil
}

// Active is the holds that block Promote for project at now (project "" means any project), soonest ending first.
func (s Store) Active(project string, now time.Time) ([]Hold, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Hold
	for _, h := range all {
		if h.ActiveAt(now) && h.Covers(project) {
			out = append(out, h)
		}
	}
	for i := 1; i < len(out); i++ { // insertion sort: a handful of holds
		for j := i; j > 0 && out[j].End.Before(out[j-1].End); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// NewHold is what Add needs to make a hold.
type NewHold struct {
	Project string
	Reason  string
	Owner   string
	Start   time.Time // zero means now
	End     time.Time
}

var projectIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidProject says whether p is a project id (the dashboard's rule: 1 to 40 lower case letters, digits and dashes) or
// "all".
func ValidProject(p string) bool { return p == AllProjects || projectIDRe.MatchString(p) }

// CleanText checks a piece of text a person typed (a reason or a name): trimmed, one line, no control characters, at
// most max characters. empty says whether it may be empty.
func CleanText(what, s string, maxLen int, empty bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" && !empty {
		return "", fmt.Errorf("%s is needed", what)
	}
	if n := len([]rune(s)); n > maxLen {
		return "", fmt.Errorf("%s is %d characters; at most %d", what, n, maxLen)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s must be one line of plain text (no tabs, line breaks or control characters)", what)
		}
	}
	return s, nil
}

// Add makes a hold and saves it. It refuses a window that is already over or ends before it starts.
func (s Store) Add(n NewHold, now time.Time) (Hold, error) {
	now = now.UTC().Truncate(time.Second)
	if !ValidProject(n.Project) {
		return Hold{}, fmt.Errorf("project %q is not a project id (lower case letters, digits and dashes) or %q", n.Project, AllProjects)
	}
	reason, err := CleanText("a reason", n.Reason, MaxReason, false)
	if err != nil {
		return Hold{}, err
	}
	owner, err := CleanText("an owner", n.Owner, MaxPerson, false)
	if err != nil {
		return Hold{}, err
	}
	start := n.Start.UTC().Truncate(time.Second)
	if n.Start.IsZero() {
		start = now
	}
	end := n.End.UTC().Truncate(time.Second)
	if n.End.IsZero() {
		return Hold{}, errors.New("an end time is needed (--until)")
	}
	if !end.After(now) {
		return Hold{}, fmt.Errorf("the end time %s is not in the future", end.Format(time.RFC3339))
	}
	if !end.After(start) {
		return Hold{}, errors.New("the end time must be after the start time")
	}
	h := Hold{ID: newID(now), Project: n.Project, Reason: reason, Owner: owner, Start: start, End: end, Created: now}
	err = s.update(func(holds []Hold) ([]Hold, error) { return append(holds, h), nil })
	if err != nil {
		return Hold{}, err
	}
	return h, nil
}

// Lift ends a hold early and records who did it. Lifting a hold that is already lifted or already over is an error, so
// nobody believes they ended a hold that was not in force.
func (s Store) Lift(id, by, reason string, now time.Time) (Hold, error) {
	now = now.UTC().Truncate(time.Second)
	by, err := CleanText("the name of who lifts it", by, MaxPerson, false)
	if err != nil {
		return Hold{}, err
	}
	reason, err = CleanText("a reason", reason, MaxReason, true)
	if err != nil {
		return Hold{}, err
	}
	var out Hold
	err = s.update(func(holds []Hold) ([]Hold, error) {
		for i := range holds {
			if holds[i].ID != id {
				continue
			}
			switch holds[i].StateAt(now) {
			case StateLifted:
				return nil, fmt.Errorf("hold %s was already lifted by %s at %s", id, holds[i].LiftedBy, holds[i].LiftedAt.Format(time.RFC3339))
			case StateEnded:
				return nil, fmt.Errorf("hold %s already ended at %s", id, holds[i].End.Format(time.RFC3339))
			}
			holds[i].LiftedBy, holds[i].LiftedAt, holds[i].LiftReason = by, now, reason
			out = holds[i]
			return holds, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	})
	return out, err
}

// update runs change on the holds under the store lock and saves the result atomically.
func (s Store) update(change func([]Hold) ([]Hold, error)) error {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	unlock, err := lock(s.Path() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	holds, err := s.List()
	if err != nil {
		return err
	}
	holds, err = change(holds)
	if err != nil {
		return err
	}
	if holds == nil {
		holds = []Hold{}
	}
	return writeAtomic(s.Path(), file{Version: Version, Holds: holds})
}

func newID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "h-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+hex.EncodeToString(rb[:])+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := retryFS(func() error { return os.Rename(tmp, path) }); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
