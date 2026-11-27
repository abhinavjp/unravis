// Package activitylog is the append-only record of what people and programs did to the release flow: who (the actor
// and the person), what (the action and its target), when and why. Every action from the UI, the CLI or Claude writes
// one entry; nothing edits or removes an entry.
//
// The log is one JSON object per line (JSONL) in the CI store folder (activity.jsonl). Append opens the file with
// O_APPEND and writes each entry with ONE write call, so entries from processes writing at the same time never mix
// inside a line. Read skips a line it cannot parse (a torn last line after a crash) instead of failing.
//
// Entries hold no secrets: text fields are one plain line, capped, and the target, reason and detail are passed through the secret masker.
package activitylog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"api-audit-explorer/internal/secretscrub"
)

// FileName is the log file in the CI store folder.
const FileName = "activity.jsonl"

// Actors: who carried out the action.
const (
	ActorUI     = "ui"
	ActorCLI    = "cli"
	ActorClaude = "claude"
)

// Actors is every valid actor.
var Actors = []string{ActorUI, ActorCLI, ActorClaude}

// ValidActor says whether a is one of Actors.
func ValidActor(a string) bool {
	for _, v := range Actors {
		if a == v {
			return true
		}
	}
	return false
}

// Caps on the text of one entry, so a line stays well under one write of any file system.
const (
	maxShort  = 80  // person, action
	maxTarget = 120 // target
	maxReason = 300
	maxLine   = 4096
)

// Entry is one line of the log. The JSON names are part of the file format and the CLI's --json output: add fields, do
// not rename or drop them.
type Entry struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`            // ui, cli or claude
	Person string    `json:"person,omitempty"` // the person behind the actor, when known
	Action string    `json:"action"`           // for example hold.add, hold.lift
	Target string    `json:"target,omitempty"` // what it acted on: a hold id, a project, a run
	Reason string    `json:"reason,omitempty"`
	// Outcome is how the action ended: done, refused, failed, invalid or error. Empty in entries written before it
	// was recorded, which were all done.
	Outcome string `json:"outcome,omitempty"`
	// Detail is the plain-words error of an action that did not end done.
	Detail string `json:"detail,omitempty"`
}

// Log is the activity log of one CI store folder.
type Log struct {
	Dir string // the CI store folder; the file is Dir/activity.jsonl
}

// Path is the log file.
func (l Log) Path() string { return filepath.Join(l.Dir, FileName) }

// oneLine makes s one trimmed line of at most n characters: control characters become spaces.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

// Append adds e to the log. A zero Time is now. The actor must be valid and the action set.
func (l Log) Append(e Entry) error {
	if !ValidActor(e.Actor) {
		return fmt.Errorf("actor %q is not one of %s", e.Actor, strings.Join(Actors, ", "))
	}
	e.Action = oneLine(e.Action, maxShort)
	if e.Action == "" {
		return errors.New("an activity entry needs an action")
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Time = e.Time.UTC().Truncate(time.Millisecond)
	e.Person = oneLine(e.Person, maxShort)
	e.Target = oneLine(secretscrub.Mask(e.Target, "***"), maxTarget)
	e.Outcome = oneLine(e.Outcome, maxShort)
	e.Detail = oneLine(secretscrub.Mask(e.Detail, "***"), maxReason)
	e.Reason = oneLine(secretscrub.Mask(e.Reason, "***"), maxReason)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if len(b) > maxLine {
		return errors.New("the activity entry is too long")
	}
	if err := os.MkdirAll(l.Dir, 0o750); err != nil {
		return err
	}
	var f *os.File
	err = retryFS(func() (e error) {
		f, e = os.OpenFile(l.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		return e
	})
	if err != nil {
		return fmt.Errorf("open the activity log: %w", err)
	}
	_, werr := f.Write(b) // one write per entry: O_APPEND puts it at the end in one piece
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write the activity log: %w", werr)
	}
	return nil
}

// Filter picks entries for Read.
type Filter struct {
	Actor string // only this actor; "" is every actor
	Limit int    // only the newest Limit entries; 0 is all
}

// Read returns the entries that pass f, oldest first. A missing log has no entries. A line that is not a valid entry is
// skipped and counted in skipped.
func (l Log) Read(f Filter) (entries []Entry, skipped int, err error) {
	var file *os.File
	err = retryFS(func() (e error) { file, e = os.Open(l.Path()); return e })
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("open the activity log: %w", err)
	}
	defer func() { _ = file.Close() }()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil || e.Action == "" || e.Time.IsZero() {
			skipped++
			continue
		}
		if f.Actor != "" && e.Actor != f.Actor {
			continue
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, skipped, fmt.Errorf("read the activity log: %w", err)
	}
	if f.Limit > 0 && len(entries) > f.Limit {
		entries = entries[len(entries)-f.Limit:]
	}
	return entries, skipped, nil
}

func retryFS(op func() error) error {
	delay := time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := op()
		if err == nil || errors.Is(err, fs.ErrNotExist) || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 40*time.Millisecond {
			delay *= 2
		}
	}
}

func transient(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "used by another process") || strings.Contains(m, "sharing violation")
}
