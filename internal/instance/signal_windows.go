//go:build windows

package instance

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"

	"golang.org/x/sys/windows"
)

// The events carry no data: a signal is only "set". Both events are created
// with the process's default security descriptor, which grants access to the
// user, SYSTEM and Administrators only. Nothing here widens it.

// listener owns the three handles and the single goroutine that waits on them.
type listener struct {
	mu     sync.Mutex // guards closed and every use of the stop handle
	closed bool

	open, quit, stopEv windows.Handle

	done     chan struct{}
	stopOnce sync.Once
	unhook   func() bool
}

// signalStop wakes the goroutine. It never touches a handle after cleanup.
func (l *listener) signalStop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		_ = windows.SetEvent(l.stopEv)
	}
}

// cleanup closes all three handles exactly once; only the goroutine calls it.
func (l *listener) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for _, h := range []windows.Handle{l.open, l.quit, l.stopEv} {
		_ = windows.CloseHandle(h)
	}
}

func (l *listener) stop() {
	l.stopOnce.Do(func() {
		if l.unhook != nil {
			l.unhook()
		}
		l.signalStop()
	})
	<-l.done
}

// run waits for the stop, open and quit events. The stop event is first so
// that, when several are set at once, stop wins and no callback runs after a
// stop was requested.
func (l *listener) run(onOpen, onQuit func()) {
	defer close(l.done)
	defer l.cleanup()
	handles := []windows.Handle{l.stopEv, l.open, l.quit}
	for {
		ev, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			return
		}
		switch ev {
		case windows.WAIT_OBJECT_0:
			return
		case windows.WAIT_OBJECT_0 + 1:
			callSafely("open", onOpen)
		case windows.WAIT_OBJECT_0 + 2:
			callSafely("quit", onQuit)
		default:
			return
		}
	}
}

// reportPanic records a recovered callback panic. The default writes to the
// standard logger, which the app routes into its sanitized log file. A seam so
// tests can observe it.
var reportPanic = func(what string, v any, stack []byte) {
	log.Printf("instance: the %s callback panicked: %v\n%s", what, v, stack)
}

// callSafely runs f and recovers a panic so one bad callback cannot end the
// listener or the process. The panic is reported, never silently dropped: a
// broken Open handler would otherwise make "open Unravis" do nothing with no
// trace. what names the callback in the report.
func callSafely(what string, f func()) {
	if f == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			reportPanic(what, v, debug.Stack())
		}
	}()
	f()
}

// createEvent makes (or opens) the named auto-reset event. existed is true when
// the name was already taken; then h is a handle to somebody's event.
func createEvent(name string) (h windows.Handle, existed bool, err error) {
	p, err := utf16Name(name)
	if err != nil {
		return 0, false, err
	}
	h, err = windows.CreateEvent(nil, 0, 0, p)
	switch {
	case err == nil:
		return h, false, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		return h, true, nil
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return 0, false, ErrAccessDenied
	}
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
	return 0, false, fmt.Errorf("instance: create event: %w", err)
}

// startListener creates the events and starts the goroutine.
func startListener(ctx context.Context, names Names, onOpen, onQuit func()) (*listener, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if names.Open == names.Quit {
		return nil, errors.New("instance: open and quit event names must differ")
	}
	openH, openExisted, err := createEvent(names.Open)
	if err != nil {
		return nil, err
	}
	quitH, quitExisted, err := createEvent(names.Quit)
	if err != nil {
		_ = windows.CloseHandle(openH)
		return nil, err
	}
	if openExisted || quitExisted {
		// Never adopt events someone else made. Decide between "ours from an
		// earlier copy" and "somebody else's" by owner, then let go of both.
		heldByOther := false
		if openExisted {
			if ok, oerr := ownerCheck(openH); oerr != nil || !ok {
				heldByOther = true
			}
		}
		if quitExisted {
			if ok, oerr := ownerCheck(quitH); oerr != nil || !ok {
				heldByOther = true
			}
		}
		_ = windows.CloseHandle(openH)
		_ = windows.CloseHandle(quitH)
		if heldByOther {
			return nil, ErrNameHeldByOther
		}
		return nil, ErrEventsTaken
	}
	// Private manual-reset stop event, no name.
	stopH, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(openH)
		_ = windows.CloseHandle(quitH)
		return nil, fmt.Errorf("instance: create stop event: %w", err)
	}
	l := &listener{open: openH, quit: quitH, stopEv: stopH, done: make(chan struct{})}
	l.unhook = context.AfterFunc(ctx, l.signalStop)
	go l.run(onOpen, onQuit)
	return l, nil
}

// Listen starts listening for Open and Quit signals sent with Signal. onOpen
// and onQuit run one at a time on a single goroutine, so callbacks must not
// block; repeated signals merge. Cancelling ctx or calling stop ends the
// listener and releases the events; stop waits for the goroutine and is safe to
// call more than once. If the events already exist Listen returns
// ErrEventsTaken, or ErrNameHeldByOther when somebody else owns them.
func Listen(ctx context.Context, names Names, onOpen, onQuit func()) (stop func(), err error) {
	l, err := startListener(ctx, names, onOpen, onQuit)
	if err != nil {
		return func() {}, err
	}
	return l.stop, nil
}

// Signal sends k to the running copy. The event handle is closed before it
// returns, so a long-lived sender never keeps the event alive. The signal is
// never sent to an object owned by somebody else.
func Signal(names Names, k Kind) error {
	var name string
	switch k {
	case Open:
		name = names.Open
	case Quit:
		name = names.Quit
	default:
		return fmt.Errorf("instance: unknown signal kind %d", int(k))
	}
	p, err := utf16Name(name)
	if err != nil {
		return err
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.READ_CONTROL, false, p)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return ErrNoInstance
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return ErrAccessDenied
		}
		return fmt.Errorf("instance: open event: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := requireOwner(h); err != nil {
		return err
	}
	if err := windows.SetEvent(h); err != nil {
		return fmt.Errorf("instance: signal event: %w", err)
	}
	return nil
}
