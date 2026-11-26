package main

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	// reopenMinGap is the shortest time between two browser opens, however many
	// requests arrive.
	reopenMinGap = 2 * time.Second
	// reopenCodeLife is how long the code in a link we open ourselves stays valid.
	reopenCodeLife = 2 * time.Minute
)

// system32File returns the full path of a file in the Windows System32 folder,
// so a planted file of the same name in the current folder or on PATH is never
// started.
func system32File(name string) string { return windowsFile("System32", name) }

// reopener answers "open Unravis" requests (a second launch or the tray): it
// mints a fresh one-time launch code, re-arms the running server with it and
// opens the browser. The code and the link are only ever handed to rearm and
// open; they are never printed, written down or put in a message.
type reopener struct {
	base    string
	rearm   func(code string, until time.Time) bool
	newCode func() (string, error)
	open    func(url string) error
	warn    func(title, text string)
	now     func() time.Time
	link    func(code string) string

	mu      sync.Mutex
	last    time.Time // when the last open that worked finished its work
	busy    bool      // an open is in progress: a second request at the same moment is dropped
	warning atomic.Bool
}

// Reopen handles one open request. A request that arrives while another is being served, or closer than reopenMinGap to the last
// open that worked, is dropped. An open that failed does not start the gap: the person can try again at once.
func (r *reopener) Reopen() {
	r.mu.Lock()
	if r.busy || r.now().Sub(r.last) < reopenMinGap {
		r.mu.Unlock()
		return
	}
	r.busy = true
	r.mu.Unlock()

	// Cleared in a defer so a panic in the work (callers recover it) cannot leave busy set for the life of the process.
	worked := false
	defer func() {
		r.mu.Lock()
		r.busy = false
		if worked {
			r.last = r.now()
		}
		r.mu.Unlock()
	}()
	worked = r.reopen()
}

// reopen does the work of one request and says whether the browser was opened.
func (r *reopener) reopen() bool {
	code, err := r.newCode()
	if err != nil {
		r.notify("Unravis", "Could not prepare a new link. Try again in a moment.")
		return false
	}
	if !r.rearm(code, r.now().Add(reopenCodeLife)) {
		r.notify("Unravis", "Unravis is still starting; try again in a moment.")
		return false
	}
	if err := r.open(r.link(code)); err != nil {
		r.notify("Unravis could not open your browser",
			"Unravis is running at "+r.base+".\n\nYour browser could not be opened. Right-click the Unravis icon in the notification area and choose \"Open Unravis\" to try again.")
		return false
	}
	return true
}

// notify shows a message box on its own goroutine, one at a time, so a modal box
// never blocks the signal listener or the tray thread.
func (r *reopener) notify(title, text string) {
	if r.warning.CompareAndSwap(false, true) {
		go func() {
			defer r.warning.Store(false)
			r.warn(title, text)
		}()
	}
}
