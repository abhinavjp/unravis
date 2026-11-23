// Package locator is the rendezvous of the desktop sidecar (BL-302). The one Go owner per user writes backend.json into the per-user
// run folder; the shell, the CLI and MCP read it to find the pipes, and start the owner when it is not running.
//
// The file holds pipe names, pid, process start time, exe path and version. It has no token field (docs/desktop/PLAN.md section 1,
// spec 02 row 2.22): the pipes are secured by pipesec and the session token travels only in requests. The file is owner-only (the
// caller passes the ACL function, so this package stays portable) and is replaced atomically. The singleton mutex, not this file, decides
// who the owner is: a file left by a dead owner is stale, and the next owner overwrites it.
//
// Alive is not authentication. It proves that a process with the recorded pid and creation time runs and that two pipe names exist; it
// does not prove that this process is the server of those pipes. Pipe names are listed for every user by \\.\pipe\ enumeration, and
// FILE_FLAG_FIRST_PIPE_INSTANCE keeps a squatter out only while the real owner lives. A caller (Attach users such as the CLI, MCP and the
// shell) must therefore check the server identity when it connects, with GetNamedPipeServerProcessId against Record.PID (or the
// pipesec client check), before it sends a token or any request.
//
// Everything here is portable. Only the process and pipe probes (Prober) and the detached start are Windows code.
package locator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"api-audit-explorer/internal/ownerlease"
)

// Schema is the only locator version this build reads and writes.
const Schema = 2

// File names. A demo run uses its own file so it never overwrites the locator of the real app (it also has its own singleton mutex).
const (
	FileName     = "backend.json"
	DemoFileName = "backend.demo.json"
)

const (
	maxFileBytes = 64 << 10
	startSlack   = time.Second
)

// Errors.
var (
	ErrNoLocator     = errors.New("locator: no locator file")
	ErrUnknownSchema = errors.New("locator: unknown locator version")
	ErrInvalid       = errors.New("locator: invalid locator file")
	ErrNoOwner       = errors.New("locator: no running owner")
)

var pipeName = regexp.MustCompile(`^\\\\\.\\pipe\\unravis\.(ui|ag)\.([0-9a-f]{32})$`)

// Record is the content of backend.json.
type Record struct {
	Version    int       `json:"version"`
	PID        int       `json:"pid"`
	StartTime  time.Time `json:"startTime"`
	Exe        string    `json:"exe"`
	AppVersion string    `json:"appVersion"`
	UI         string    `json:"ui"`
	AG         string    `json:"ag"`
}

// Validate checks the shape. A reader never trusts a field it did not check: names must be unravis pipes of the right role.
func (r Record) Validate() error {
	if r.Version != Schema {
		return fmt.Errorf("%w: %d", ErrUnknownSchema, r.Version)
	}
	if r.PID <= 0 || r.StartTime.IsZero() {
		return fmt.Errorf("%w: pid or start time", ErrInvalid)
	}
	if !pipeName.MatchString(r.UI) || !hasRole(r.UI, "ui") {
		return fmt.Errorf("%w: ui pipe name", ErrInvalid)
	}
	if !pipeName.MatchString(r.AG) || !hasRole(r.AG, "ag") {
		return fmt.Errorf("%w: ag pipe name", ErrInvalid)
	}
	// One owner makes both names from one nonce (pipesec.NewNamePair).
	if pipeName.FindStringSubmatch(r.UI)[2] != pipeName.FindStringSubmatch(r.AG)[2] {
		return fmt.Errorf("%w: ui and ag pipe names have different nonces", ErrInvalid)
	}
	return nil
}

func hasRole(name, role string) bool {
	m := pipeName.FindStringSubmatch(name)
	return len(m) == 3 && m[1] == role
}

// FilePath is the locator path inside runDir.
func FilePath(runDir string, demo bool) string {
	if demo {
		return filepath.Join(runDir, DemoFileName)
	}
	return filepath.Join(runDir, FileName)
}

// Options are the owner-only ACL steps, injected so the package is portable. A nil function means "do nothing" (tests off Windows).
type Options struct {
	SecureDir  func(dir string) error
	SecureFile func(path string) error
}

// Write replaces the locator at path atomically: a temp file in the same folder is restricted to the owner before any content goes in,
// then renamed over the target. A reader sees the old file or the new one, never half of one.
func Write(path string, r Record, o Options) error {
	if err := r.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("locator: create run folder: %w", err)
	}
	if o.SecureDir != nil {
		// SecureDir sets an inheriting DACL, which rewrites the ACLs of everything below dir. Never do that to a folder that holds
		// anything but locator files (UNRAVIS_RUN_DIR can point anywhere).
		if err := onlyLocatorFiles(dir); err != nil {
			return err
		}
		if err := o.SecureDir(dir); err != nil {
			return fmt.Errorf("locator: restrict run folder: %w", err)
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("locator: create temp file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if o.SecureFile != nil {
		if err := o.SecureFile(name); err != nil {
			cleanup()
			return fmt.Errorf("locator: restrict file: %w", err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := renameRetry(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("locator: replace file: %w", err)
	}
	return nil
}

// onlyLocatorFiles fails when dir holds an entry that is not a locator file, its temp file or an owner lease file.
func onlyLocatorFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("locator: read run folder: %w", err)
	}
	for _, e := range entries {
		if n := e.Name(); e.IsDir() || (!strings.HasPrefix(n, "backend") && !ownerlease.IsLeaseFile(n)) {
			return fmt.Errorf("locator: run folder %s holds %q, which is not a locator file; refusing to restrict it", dir, n)
		}
	}
	return nil
}

// Rename retry budget. On Windows a reader that opened the target without FILE_SHARE_DELETE (Go os.Open, Get-Content, a polling
// caller) makes the replace fail until it closes the file. Readers hold it for milliseconds, so 5 s is generous; the owner cannot
// serve without a locator, so a failure after the budget is still an error.
const (
	renameTries = 100
	renameWait  = 50 * time.Millisecond
)

// renameRetry retries for renameTries x renameWait.
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < renameTries; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(renameWait)
	}
	return err
}

// Read parses and validates the locator at path. ErrNoLocator when the file does not exist; ErrUnknownSchema for another version.
func Read(path string) (Record, error) {
	f, err := os.Open(path) //nolint:gosec // the locator path is built from the run folder, not from input
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNoLocator
	}
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Record{}, err
	}
	if len(data) > maxFileBytes {
		return Record{}, fmt.Errorf("%w: file too large", ErrInvalid)
	}
	var r Record
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&r); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := r.Validate(); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Remove deletes the locator only if it still describes this owner (same pid and start time), so a new owner's file is never removed by
// the old one's late cleanup. A missing file is not an error.
func Remove(path string, own Record) error {
	cur, err := Read(path)
	if err != nil {
		if errors.Is(err, ErrNoLocator) {
			return nil
		}
		return nil // unreadable or foreign: not ours to delete
	}
	if cur.PID != own.PID || !sameStart(cur.StartTime, own.StartTime) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sameStart(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= startSlack
}

// Prober answers the two OS questions Alive needs.
type Prober interface {
	// ProcessStart returns the creation time of the live process pid, or an error when no such process runs.
	ProcessStart(pid int) (time.Time, error)
	// PipeExists reports whether a named pipe of that name exists now.
	PipeExists(name string) bool
}

// Alive reports whether r describes a running owner: the pid is running and was created when the record says (a reused pid has a
// different creation time), and both pipes exist.
func Alive(r Record, p Prober) bool {
	if r.Validate() != nil {
		return false
	}
	start, err := p.ProcessStart(r.PID)
	if err != nil || !sameStart(start, r.StartTime) {
		return false
	}
	return p.PipeExists(r.UI) && p.PipeExists(r.AG)
}

// Wait polls the locator at path until it describes a live owner, at most tries times with wait between. It returns false on timeout.
func Wait(path string, p Prober, tries int, wait time.Duration, sleep func(time.Duration)) (Record, bool) {
	for i := 0; i < tries; i++ {
		if r, err := Read(path); err == nil && Alive(r, p) {
			return r, true
		}
		if i < tries-1 {
			sleep(wait)
		}
	}
	return Record{}, false
}

// AttachOptions configure Attach.
type AttachOptions struct {
	Path   string
	Prober Prober
	// Start launches the owner (detached, no window). Nil means attach only.
	Start func() error
	Sleep func(time.Duration)
	// Tries x Wait is how long to wait for the owner to publish a live locator after Start. Defaults 40 x 250 ms (spec 01 BL-302).
	Tries int
	Wait  time.Duration
}

// Attach finds the running owner or starts one. started is true when this call launched the owner. Ten callers at once are safe: each
// may launch a copy, but the singleton gate (internal/instance, held for the owner's lifetime) lets one copy become the owner and the
// others exit, and every caller then finds the one live locator.
func Attach(o AttachOptions) (r Record, started bool, err error) {
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Tries <= 0 {
		o.Tries = 40
	}
	if o.Wait <= 0 {
		o.Wait = 250 * time.Millisecond
	}
	if cur, rerr := Read(o.Path); rerr == nil && Alive(cur, o.Prober) {
		return cur, false, nil
	}
	if o.Start == nil {
		return Record{}, false, ErrNoOwner
	}
	if err := o.Start(); err != nil {
		return Record{}, false, fmt.Errorf("locator: start owner: %w", err)
	}
	if cur, ok := Wait(o.Path, o.Prober, o.Tries, o.Wait, o.Sleep); ok {
		return cur, true, nil
	}
	return Record{}, true, ErrNoOwner
}
