// Package crashlog keeps a local record of a Go crash (BL-315, spec 01 D11): the fatal error text and goroutine stacks the runtime prints
// when the program panics without recovering or hits a fatal runtime error.
//
// Local only. Nothing here opens a socket, calls an upload or reports home; the files stay in the crash folder (appdirs.CrashDir) and
// the person sends one by hand if they choose to.
//
// How it works, and why it takes two steps:
//
//  1. Start opens an owner-only file named crash-<UTC>-<pid>-go.pending and gives it to runtime/debug.SetCrashOutput. The runtime writes
//     the crash text to that file from low-level code, as raw bytes, with no chance for Go code to filter it. Panic messages can carry
//     a secret (an error that wrapped a connection string), so the raw file is never the file the person is told to share.
//  2. The next Start (the next run of the program, on any copy) finds a non-empty .pending file, reads it, runs it through
//     applog.Sanitize and writes crash-<UTC>-<pid>-go.txt (owner-only), then deletes the raw file. If that step fails the raw file stays
//     (owner-only) and is tried again at the next start. A clean exit (Capture.Close) removes the pending file when nothing was written
//     to it; an abrupt exit leaves an empty file that the next start removes.
//
// What the dump holds: the panic or fatal message, then the stacks of every goroutine (GOTRACEBACK is raised to "all" by Start;
// the environment may only raise it further, never lower it). Stack frames show function names, file paths and argument words; heap
// contents are not in a Go crash text. "crash" (core dump, which on Windows hands the process to WER) is deliberately not used.
//
// Retention: after the sweep, Start keeps the newest MaxFiles finished crash files (by modification time) and at most MaxBytes of them,
// and deletes the rest. Only files this package and the Rust half name crash-*.txt / crash-*.dmp are ever pruned; nothing else in the
// folder is touched.
package crashlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/applog"
)

const (
	// DefaultMaxFiles is how many finished crash files are kept (spec 01 D11).
	DefaultMaxFiles = 10
	// DefaultMaxBytes is the most disk the finished crash files may use (spec 01 D11).
	DefaultMaxBytes int64 = 50 << 20
	// DefaultPendingGrace is how young a .pending file must be to be left alone: it may belong to a copy that is running right now (or
	// that is still writing its dying breath), so the sweep does not read or delete it.
	DefaultPendingGrace = 30 * time.Second
	// MaxReadBytes caps how much of one raw dump is read; a longer dump is cut and says so. A dump of a few thousand goroutines is well
	// under it.
	MaxReadBytes = 4 << 20

	// TracebackLevel is what Start asks the runtime for: the panicking goroutine and all others (not the runtime-internal ones).
	TracebackLevel = "all"

	pendingExt  = ".pending"
	finishedExt = ".txt"
	filePrefix  = "crash-"
	goSuffix    = "-go"
	cutNote     = "\n[crash text cut here: it was longer than the read limit]\n"
)

// Options configures Start. The zero value of every field but Dir means the default.
type Options struct {
	Dir      string // the crash folder (appdirs.CrashDir); required
	MaxFiles int    // finished crash files kept; 0 = DefaultMaxFiles
	MaxBytes int64  // bytes they may use together; 0 = DefaultMaxBytes
	// Grace is the age under which a .pending file is left alone; 0 = DefaultPendingGrace, negative = no grace (tests).
	Grace time.Duration
	// SecureDir and SecureFile make a folder and a file owner-only (casestore.SecureDir / SecureFile in the app). Nil leaves the
	// mode the OS gave (0700 / 0600 are still requested at creation on Unix).
	SecureDir  func(string) error
	SecureFile func(string) error
	// Now is the clock (tests). Nil = time.Now.
	Now func() time.Time
}

// Capture is a running crash capture. A nil *Capture is valid and does nothing.
type Capture struct {
	opts Options
	path string // the .pending file this run writes
	file *os.File
}

var (
	currentMu sync.Mutex
	current   *Capture // the capture that owns the runtime's single crash output, if any
)

// Start prepares the crash folder, finishes and prunes what earlier runs left, then points the runtime's crash output at a new pending
// file. It returns an error only to be logged: a program that cannot capture crashes must still run. The returned *Capture is non-nil
// whenever the crash output was installed.
func Start(opts Options) (*Capture, error) {
	if opts.Dir == "" {
		return nil, errors.New("crashlog: no crash folder")
	}
	opts = opts.withDefaults()
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("crashlog: creating the crash folder: %w", err)
	}
	if opts.SecureDir != nil {
		if err := opts.SecureDir(opts.Dir); err != nil {
			return nil, fmt.Errorf("crashlog: securing the crash folder: %w", err)
		}
	}
	// The sweep and the prune are housekeeping: their trouble is reported but does not stop the capture from starting.
	var problems []error
	if err := Sweep(opts); err != nil {
		problems = append(problems, err)
	}
	if err := Prune(opts); err != nil {
		problems = append(problems, err)
	}
	c, err := open(opts)
	if err != nil {
		return nil, errors.Join(append(problems, err)...)
	}
	// debug.SetTraceback can only raise the level the GOTRACEBACK variable gave, never lower it.
	debug.SetTraceback(TracebackLevel)
	return c, errors.Join(problems...)
}

func (o Options) withDefaults() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Grace == 0 {
		o.Grace = DefaultPendingGrace
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// open creates this run's pending file and installs it as the runtime's crash output.
func open(opts Options) (*Capture, error) {
	stamp := opts.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(opts.Dir, fmt.Sprintf("%s%s-%d%s%s", filePrefix, stamp, os.Getpid(), goSuffix, pendingExt))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file named by this package in the crash folder
	if err != nil {
		return nil, fmt.Errorf("crashlog: creating the crash file: %w", err)
	}
	if opts.SecureFile != nil {
		if err := opts.SecureFile(path); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("crashlog: securing the crash file: %w", err)
		}
	}
	// SetCrashOutput duplicates the handle, so this copy is closed again at once and the runtime keeps its own.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("crashlog: installing the crash output: %w", err)
	}
	_ = f.Close()
	c := &Capture{opts: opts, path: path}
	currentMu.Lock()
	current = c
	currentMu.Unlock()
	return c, nil
}

// Close stops capturing and removes this run's pending file if nothing was written to it (the normal case: no crash). It is safe on a
// nil Capture and safe to call twice. It only releases the runtime's crash output if this capture still owns it.
func (c *Capture) Close() {
	if c == nil {
		return
	}
	currentMu.Lock()
	owner := current == c
	if owner {
		current = nil
	}
	currentMu.Unlock()
	if owner {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{}) // closes the runtime's handle, which Windows needs before a delete
	}
	if c.path == "" {
		return
	}
	if info, err := os.Stat(c.path); err == nil && info.Size() == 0 {
		_ = os.Remove(c.path)
	}
	c.path = ""
}

// CloseUnlessPanic is Close for a defer in the program's top function: while a panic is unwinding it leaves the capture open, so the
// runtime can still write the dump to the pending file when it prints the panic. It must be the deferred call itself (recover only works
// there); the panic is raised again unchanged, so the original stack is in the trace.
func (c *Capture) CloseUnlessPanic() {
	if r := recover(); r != nil {
		panic(r)
	}
	c.Close()
}

// Path is the pending file this run writes (empty after Close); tests and the log line use it.
func (c *Capture) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Sweep turns every old non-empty .pending file into a sanitised crash-...-go.txt and removes the raw file, and removes old empty ones.
// A file younger than Options.Grace is skipped. It returns the joined errors of the files it could not finish.
func Sweep(opts Options) error {
	opts = opts.withDefaults()
	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		return fmt.Errorf("crashlog: reading the crash folder: %w", err)
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, pendingExt) {
			continue
		}
		path := filepath.Join(opts.Dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		if opts.Grace > 0 && opts.Now().Sub(info.ModTime()) < opts.Grace {
			continue
		}
		if info.Size() == 0 {
			// On Windows a file still open by a running copy cannot be deleted, so this leaves a live copy's file alone.
			_ = os.Remove(path)
			continue
		}
		if err := finish(opts, path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// finish writes the sanitised copy of one raw dump and removes the raw file.
func finish(opts Options, raw string) error {
	info, err := os.Stat(raw)
	if err != nil {
		return fmt.Errorf("crashlog: reading %s: %w", filepath.Base(raw), err)
	}
	text, err := readCapped(raw)
	if err != nil {
		return fmt.Errorf("crashlog: reading %s: %w", filepath.Base(raw), err)
	}
	out := strings.TrimSuffix(raw, pendingExt) + finishedExt
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the path is the pending file's own name in the crash folder
	if err != nil {
		return fmt.Errorf("crashlog: creating %s: %w", filepath.Base(out), err)
	}
	if opts.SecureFile != nil {
		if err := opts.SecureFile(out); err != nil {
			_ = f.Close()
			_ = os.Remove(out) // never leave a sanitised file with the default ACL next to the raw one
			return fmt.Errorf("crashlog: securing %s: %w", filepath.Base(out), err)
		}
	}
	_, werr := io.WriteString(f, applog.Sanitize(text))
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("crashlog: writing %s: %w", filepath.Base(out), err)
	}
	// The finished file keeps the time of the crash, not of the sweep, so retention order is the order the crashes happened in.
	_ = os.Chtimes(out, info.ModTime(), info.ModTime())
	if err := os.Remove(raw); err != nil {
		return fmt.Errorf("crashlog: removing %s: %w", filepath.Base(raw), err)
	}
	return nil
}

func readCapped(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a file of the crash folder found by the sweep
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only
	b, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > MaxReadBytes {
		return string(b[:MaxReadBytes]) + cutNote, nil
	}
	return string(b), nil
}

// Prune keeps the newest MaxFiles finished crash files (crash-*.txt and crash-*.dmp) that also fit in MaxBytes together, newest first,
// and deletes the rest. Newest is by modification time, then by name. The newest file is always kept, even alone over the byte limit.
func Prune(opts Options) error {
	opts = opts.withDefaults()
	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		return fmt.Errorf("crashlog: reading the crash folder: %w", err)
	}
	type item struct {
		path string
		name string
		mod  time.Time
		size int64
	}
	var files []item
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if e.IsDir() || !strings.HasPrefix(name, filePrefix) || (ext != finishedExt && ext != ".dmp") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, item{filepath.Join(opts.Dir, name), name, info.ModTime(), info.Size()})
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].name > files[j].name
	})
	var errs []error
	var used int64
	for i, f := range files {
		used += f.size
		if i < opts.MaxFiles && (i == 0 || used <= opts.MaxBytes) {
			continue
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("crashlog: pruning %s: %w", f.name, err))
		}
	}
	return errors.Join(errs...)
}
