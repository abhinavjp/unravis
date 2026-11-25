package main

import (
	"errors"
	"time"

	"api-audit-explorer/internal/instance"
	"api-audit-explorer/internal/locator"
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
	// A pipe-mode copy that is not the owner waits this long for the owner's locator to go live (spec 01 BL-302: 40 x 250 ms).
	gateLocatorTries = 40
	// exitAttached is the exit code of a pipe-mode copy that found a live owner: nothing is wrong, the caller attaches through the locator.
	exitAttached = 12
	// Pipe mode has no console and no window, so its failures are exit codes and never a message box (the caller is a shell, the CLI or
	// MCP, and reads the code). exitElevated: the copy runs as administrator (BL-304). exitNoOwner: another copy holds the singleton
	// but no live locator appeared within the wait, for example because that copy is browser mode (it writes none) or is still starting.
	exitElevated = 11
	exitNoOwner  = 13
	// exitOtherSession: the owner lease is held by a copy in another sign-in session of this Windows profile. Waiting does not help; that
	// copy has to be quit in its own session.
	exitOtherSession = 14
)

// refuseElevated is the one administrator check. Today enterSingleInstance (the
// interactive start) calls it before it touches a name or a mutex. --mcp,
// --no-browser, --version and --quit do not reach it; the --pipe-server branch
// (BL-301, not built yet) must call it first. Setup's elevated worker only runs
// "unravis.exe --version", which returns before the gate.
func refuseElevated(elevated func() bool) error {
	if elevated() {
		return ErrElevated
	}
	return nil
}

// gateDeps are the system calls the gate makes, so tests can use fakes.
type gateDeps struct {
	names    func(app string) (instance.Names, error)
	acquire  func(instance.Names) (func(), bool, error)
	signal   func(instance.Names, instance.Kind) error
	sleep    func(time.Duration)
	elevated func() bool
	// liveLocator reports whether the locator of this run names a live owner. Only pipe mode uses it.
	liveLocator func() bool
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

// pipeGateDeps is productionGateDeps plus the locator check pipe mode needs.
func pipeGateDeps(runDir string, demo bool) gateDeps {
	d := productionGateDeps()
	path := locator.FilePath(runDir, demo)
	prober := locator.SystemProber()
	d.liveLocator = func() bool {
		r, err := locator.Read(path)
		return err == nil && locator.Alive(r, prober)
	}
	return d
}

// gateResult is the outcome of enterSingleInstance. proceed is true only for the
// first copy, which must call release when it exits.
type gateResult struct {
	release func()
	names   instance.Names
	proceed bool
	// attached is true for a pipe-mode copy that is not the owner and found the owner's live locator. The caller exits with exitAttached.
	attached bool
}

// enterSingleInstance makes sure only one interactive copy runs per user. An
// elevated copy is refused before any name or mutex is touched, so it can never
// take the name and lock a normal start out. A second copy asks the first to
// open a tab and does not proceed; a second --background copy exits silently. A second pipe-mode copy never signals Open (there is no
// tab): it waits up to 40 x 250 ms for the owner's locator to go live and reports attached, or exits quietly with exitNoOwner if none does (BL-302).
func enterSingleInstance(demo, background, pipeMode bool, d gateDeps) (gateResult, error) {
	if err := refuseElevated(d.elevated); err != nil {
		if pipeMode {
			return gateResult{}, &exitCodeError{code: exitElevated, msg: err.Error()}
		}
		return gateResult{}, err
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
	if pipeMode {
		for try := 1; try <= gateLocatorTries; try++ {
			if d.liveLocator != nil && d.liveLocator() {
				return gateResult{names: names, attached: true}, nil
			}
			if try < gateLocatorTries {
				d.sleep(gateSignalWait)
			}
		}
		return gateResult{}, &exitCodeError{code: exitNoOwner, msg: "another Unravis copy owns the name but published no live locator"}
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
