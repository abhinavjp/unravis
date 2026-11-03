package main

import (
	"errors"
	"time"

	"api-audit-explorer/internal/instance"
)

// The text of each error is the message the user sees.
var (
	ErrAlreadyRunning           = errors.New("Unravis is already running but could not be reached. Quit it from the tray icon or Task Manager and start it again.")
	ErrRunningForAnotherAccount = errors.New("Unravis is already running under another Windows account or as administrator. Quit it there, then start Unravis again.")
	ErrElevated                 = errors.New("Unravis does not run as administrator. Start it from the Start menu or a normal window.")
	ErrNameHeld                 = errors.New("Another process holds Unravis's name, so Unravis cannot start. Quit the other program or sign out of Windows and in again, then start Unravis again.")
)

const (
	// gateSignalTries x gateSignalWait is the budget for a first copy that owns
	// the mutex but has not created its events yet.
	gateSignalTries = 20
	gateSignalWait  = 250 * time.Millisecond
)

// gateDeps are the system calls the gate makes, so tests can use fakes.
type gateDeps struct {
	names    func(app string) (instance.Names, error)
	acquire  func(instance.Names) (func(), bool, error)
	signal   func(instance.Names, instance.Kind) error
	sleep    func(time.Duration)
	elevated func() bool
}

// productionGateDeps is the gate wired to the real mutex, events and clock.
func productionGateDeps() gateDeps {
	return gateDeps{
		names:    instance.CurrentNames,
		acquire:  instance.Acquire,
		signal:   instance.Signal,
		sleep:    time.Sleep,
		elevated: isElevated,
	}
}

// gateResult is the outcome of enterSingleInstance. proceed is true only for the
// first copy, which must call release when it exits.
type gateResult struct {
	release func()
	names   instance.Names
	proceed bool
}

// enterSingleInstance makes sure only one interactive copy runs per user. An
// elevated copy is refused before any name or mutex is touched, so it can never
// take the name and lock a normal start out. A second copy asks the first to
// open a tab and does not proceed; a second --background copy exits silently.
func enterSingleInstance(demo, background bool, d gateDeps) (gateResult, error) {
	if d.elevated() {
		return gateResult{}, ErrElevated
	}
	app := instance.DefaultApp
	if demo {
		app = instance.DemoApp
	}
	names, err := d.names(app)
	if err != nil {
		return gateResult{}, err
	}
	release, first, err := d.acquire(names)
	if err != nil {
		return gateResult{}, gateError(err)
	}
	if first {
		if release == nil {
			release = func() {}
		}
		return gateResult{release: release, names: names, proceed: true}, nil
	}
	if release != nil {
		release()
	}
	if background {
		return gateResult{names: names}, nil
	}
	for try := 1; ; try++ {
		err := d.signal(names, instance.Open)
		if err == nil {
			return gateResult{names: names}, nil
		}
		if !errors.Is(err, instance.ErrNoInstance) {
			return gateResult{}, gateError(err)
		}
		if try >= gateSignalTries {
			return gateResult{}, ErrAlreadyRunning
		}
		d.sleep(gateSignalWait)
	}
}

// gateError maps an instance error to the message the user should see; any
// other error passes through.
func gateError(err error) error {
	switch {
	case errors.Is(err, instance.ErrNameHeldByOther):
		return ErrNameHeld
	case errors.Is(err, instance.ErrAccessDenied):
		return ErrRunningForAnotherAccount
	}
	return err
}

// startupCode returns the launch code for this start. A background start mints
// none: a code is made only when someone asks to open (tray or second launch).
func startupCode(background bool, mint func() (string, error)) (string, error) {
	if background {
		return "", nil
	}
	return mint()
}
