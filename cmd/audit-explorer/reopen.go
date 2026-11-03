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
	last    time.Time
	warning atomic.Bool
}

// Reopen handles one open request. Requests closer than reopenMinGap to the
// last accepted one are dropped.
func (r *reopener) Reopen() {
	r.mu.Lock()
	t := r.now()
	if t.Sub(r.last) < reopenMinGap {
		r.mu.Unlock()
		return
	}
	r.last = t
	r.mu.Unlock()

	code, err := r.newCode()
	if err != nil {
		r.notify("Unravis", "Could not prepare a new link. Try again in a moment.")
		return
	}
	if !r.rearm(code, r.now().Add(reopenCodeLife)) {
		r.notify("Unravis", "Unravis is still starting; try again in a moment.")
		return
	}
	if err := r.open(r.link(code)); err != nil {
		r.notify("Unravis could not open your browser",
			"Unravis is running at "+r.base+".\n\nYour browser could not be opened. Right-click the Unravis icon in the notification area and choose \"Open Unravis\" to try again.")
	}
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
