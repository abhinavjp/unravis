package main

import (
	"context"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/tray"
)

const (
	// trayExitGuard bounds how long run waits for the tray to remove its icon.
	trayExitGuard = 2 * time.Second
	// endSessionGuard bounds how long the end-session handler waits for run to
	// finish its cleanup before Windows stops the process.
	endSessionGuard = 3 * time.Second
)

var (
	// runDone is closed by main once run has returned.
	runDone = make(chan struct{})
	// endingSession is set while Windows is ending the session.
	endingSession atomic.Bool
)

// waitFor reports whether ch was closed before the guard ran out.
func waitFor(ch <-chan struct{}, guard time.Duration) bool {
	t := time.NewTimer(guard)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

// startTray runs the tray on its own goroutine. A failure is logged once and the
// app carries on without an icon. The returned channel is closed when run
// returns, whether or not it failed.
func startTray(ctx context.Context, run func(context.Context, tray.Options) error, o tray.Options, logf func(string, ...any)) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := run(ctx, o); err != nil {
			logf("Tray icon unavailable: %v", err)
		}
	}()
	return done
}

// stopTray cancels the app and waits, bounded, for the tray to remove its icon.
// During an end-session it does not wait: the handler is already waiting on run,
// so waiting here would only burn the guard.
func stopTray(cancel func(), ending *atomic.Bool, trayDone <-chan struct{}, guard time.Duration) {
	cancel()
	if ending.Load() {
		return
	}
	waitFor(trayDone, guard)
}

// endSessionHandler returns the tray's end-session callback: it cancels the app
// and waits, bounded, until run has finished so the session file is gone before
// Windows stops the process.
func endSessionHandler(cancel func(), ending *atomic.Bool, done <-chan struct{}, guard time.Duration) func() {
	return func() {
		ending.Store(true)
		cancel()
		waitFor(done, guard)
	}
}
